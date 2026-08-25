// Package notifier holds background (non-request) jobs. Currently just the
// Task due-date email reminder — there is no other cron/scheduler
// infrastructure in this API, everything else runs synchronously per Fiber
// request.
package notifier

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// taskReminderInterval is how often the background checker looks for newly-due
// tasks. 15 minutes is frequent enough that a reminder goes out shortly after
// a task becomes due, without hammering the DB or an SMTP provider.
const taskReminderInterval = 15 * time.Minute

// StartTaskDueReminders launches a background goroutine that periodically
// emails the assignee of any open Task whose due date has passed and that
// hasn't been notified yet. Safe to call even when SMTP isn't configured —
// utils.SendMail no-ops (logs a warning) in that case rather than erroring.
//
// This is the only background job in the app; it runs on its own ticker
// rather than piggybacking on the Fiber request lifecycle since due-date
// checks aren't triggered by any specific HTTP request.
func StartTaskDueReminders(db *gorm.DB, cfg *config.Config) {
	ticker := time.NewTicker(taskReminderInterval)
	go func() {
		// Run one pass immediately on startup instead of waiting a full
		// interval for the first check.
		checkDueTasks(db, cfg)
		for range ticker.C {
			checkDueTasks(db, cfg)
		}
	}()
}

// checkDueTasks finds pending tasks that are due and not yet notified, emails
// each assignee, and stamps NotifiedAt so it isn't sent again. A failure
// sending one task's email (or resolving its assignee) is logged and does not
// stop the rest of the batch.
//
// Assignees and related Deal/Contact/Company names are resolved via a
// handful of batched queries up front (one per distinct related-entity type,
// plus one for assignees) rather than the two First()-per-task round trips
// this used to run — the difference between ~4 queries and 2*len(tasks) once
// there's a real backlog of due tasks.
func checkDueTasks(db *gorm.DB, cfg *config.Config) {
	var tasks []models.Task
	err := db.Where("status = ? AND due_date <= ? AND notified_at IS NULL", models.TaskStatusPending, time.Now()).
		Find(&tasks).Error
	if err != nil {
		log.Printf("notifier: failed to query due tasks: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	assignees := loadAssignees(db, tasks)
	relatedNames := loadRelatedNames(db, tasks)

	for _, task := range tasks {
		if err := notifyTaskDue(db, cfg, task, assignees, relatedNames); err != nil {
			log.Printf("notifier: failed to notify task %d: %v", task.ID, err)
			continue
		}
	}
}

// loadAssignees batch-resolves every distinct Task.AssignedTo in tasks into
// a userID -> User map, replacing a First() per task.
func loadAssignees(db *gorm.DB, tasks []models.Task) map[uint]models.User {
	idSet := make(map[uint]bool)
	for _, t := range tasks {
		if t.AssignedTo != nil {
			idSet[*t.AssignedTo] = true
		}
	}
	if len(idSet) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	var users []models.User
	if err := db.Where("id IN ?", ids).Find(&users).Error; err != nil {
		log.Printf("notifier: failed to batch-resolve task assignees: %v", err)
		return nil
	}
	byID := make(map[uint]models.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	return byID
}

// relatedKey identifies a Task's related record for relatedNames' map.
type relatedKey struct {
	relatedType models.TaskRelatedType
	relatedID   uint
}

// loadRelatedNames batch-resolves every distinct (RelatedType, RelatedID)
// referenced by tasks into a human-readable name, one query per type
// present instead of a First() per task. Best-effort per buildReminderBody's
// existing contract: an unresolvable entry is simply absent from the map.
func loadRelatedNames(db *gorm.DB, tasks []models.Task) map[relatedKey]string {
	dealIDs, contactIDs, companyIDs := map[uint]bool{}, map[uint]bool{}, map[uint]bool{}
	for _, t := range tasks {
		if t.RelatedID == 0 {
			continue
		}
		switch t.RelatedType {
		case models.RelatedTypeDeal:
			dealIDs[t.RelatedID] = true
		case models.RelatedTypeContact:
			contactIDs[t.RelatedID] = true
		case models.RelatedTypeCompany:
			companyIDs[t.RelatedID] = true
		}
	}

	names := make(map[relatedKey]string)
	if len(dealIDs) > 0 {
		var deals []models.Deal
		if err := db.Where("id IN ?", mapKeys(dealIDs)).Find(&deals).Error; err == nil {
			for _, d := range deals {
				names[relatedKey{models.RelatedTypeDeal, d.ID}] = d.Title
			}
		}
	}
	if len(contactIDs) > 0 {
		var contacts []models.Contact
		if err := db.Where("id IN ?", mapKeys(contactIDs)).Find(&contacts).Error; err == nil {
			for _, c := range contacts {
				names[relatedKey{models.RelatedTypeContact, c.ID}] = c.Name
			}
		}
	}
	if len(companyIDs) > 0 {
		var companies []models.Company
		if err := db.Where("id IN ?", mapKeys(companyIDs)).Find(&companies).Error; err == nil {
			for _, c := range companies {
				names[relatedKey{models.RelatedTypeCompany, c.ID}] = c.Name
			}
		}
	}
	return names
}

func mapKeys(m map[uint]bool) []uint {
	ids := make([]uint, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	return ids
}

// notifyTaskDue resolves the assignee's email, sends the reminder, and marks
// the task as notified. Returns an error (logged by the caller) if the
// assignee isn't in the pre-loaded map or the update fails — a send failure
// from utils.SendMail itself is also surfaced here so the caller logs it and
// moves on to the next task.
func notifyTaskDue(db *gorm.DB, cfg *config.Config, task models.Task, assignees map[uint]models.User, relatedNames map[relatedKey]string) error {
	if task.AssignedTo == nil {
		// Nothing to notify — mark it done so it's not re-checked forever.
		return markNotified(db, task)
	}

	assignee, ok := assignees[*task.AssignedTo]
	if !ok {
		return fmt.Errorf("resolve assignee %d: not found", *task.AssignedTo)
	}
	if assignee.Email == "" {
		return markNotified(db, task)
	}

	subject := fmt.Sprintf("Task due: %s", task.Title)
	body := buildReminderBody(task, relatedNames)

	if err := utils.SendMail(cfg, assignee.Email, subject, body); err != nil {
		return fmt.Errorf("send reminder email: %w", err)
	}

	return markNotified(db, task)
}

// buildReminderBody assembles a simple plain-text email, including the
// related Deal/Contact/Company name when it was resolved in relatedNames. A
// missing entry just omits that line rather than blocking the email.
func buildReminderBody(task models.Task, relatedNames map[relatedKey]string) string {
	body := fmt.Sprintf(
		"Reminder: the following task is due.\n\nTask: %s\nDue date: %s\n",
		task.Title, task.DueDate.Format("2006-01-02 15:04"),
	)

	if related := relatedNames[relatedKey{task.RelatedType, task.RelatedID}]; related != "" {
		body += fmt.Sprintf("Related %s: %s\n", task.RelatedType, related)
	}

	return body
}

func markNotified(db *gorm.DB, task models.Task) error {
	now := time.Now()
	if err := db.Model(&models.Task{}).Where("id = ?", task.ID).Update("notified_at", &now).Error; err != nil {
		return fmt.Errorf("mark task %d notified: %w", task.ID, err)
	}
	return nil
}
