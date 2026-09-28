// Package notifier holds background (non-request) jobs. Currently just the
// Task due-date email reminder — there is no other cron/scheduler
// infrastructure in this API, everything else runs synchronously per Fiber
// request.
package notifier

import (
	"context"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
)

// taskReminderInterval is how often the background checker looks for newly-due
// tasks. 15 minutes is frequent enough that a reminder goes out shortly after
// a task becomes due, without hammering the DB or an SMTP provider.
const taskReminderInterval = 15 * time.Minute

// StartTaskDueReminders launches a background goroutine that periodically
// emails the assignee of any open Task whose due date has passed and that
// hasn't been notified yet. Safe to call even when SMTP isn't configured —
// utils.SendMail silently no-ops then, and the task is still stamped
// NotifiedAt, so turning SMTP on later doesn't suddenly email a backlog of
// long-past-due tasks. The task itself is the in-app alert either way.
//
// One of several background jobs (see also workflow_rules.go,
// forecast_snapshots.go, weekly_digest.go, all started from cmd/api/main.go),
// each on its own ticker rather than piggybacking on the Fiber request
// lifecycle, since none is triggered by a specific HTTP request. Each runs
// one pass immediately on startup instead of waiting a full interval, and
// stops when ctx is cancelled (see runEvery).
func StartTaskDueReminders(ctx context.Context, db *gorm.DB, cfg *config.Config) {
	runEvery(ctx, "task due reminders", taskReminderInterval, func() { checkDueTasks(db, cfg) })
}

// checkDueTasks finds pending tasks that are due and not yet notified, and
// for each one claims it (stamps NotifiedAt) and then emails the assignee.
// A failure on one task is logged and does not stop the rest of the batch.
//
// Claim-then-send, not send-then-stamp: with more than one instance
// running, two passes could both select the same task and both email it.
// The claim is a conditional UPDATE only one of them can win, so a
// reminder goes out at most once. The trade-off is that a send that fails
// after the claim isn't retried — acceptable for a reminder about a task
// that's already visible in-app, and better than the duplicate (or, on a
// permanently failing address, an email attempt every 15 minutes forever).
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

	// A lookup failure aborts the pass rather than proceeding with an empty
	// map, which would read as "every assignee is gone" and stamp the whole
	// batch handled without sending anything.
	assignees, err := loadAssignees(db, tasks)
	if err != nil {
		log.Printf("notifier: failed to batch-resolve task assignees: %v", err)
		return
	}
	relatedNames := loadRelatedNames(db, tasks)

	for _, task := range tasks {
		if err := notifyTaskDue(db, cfg, task, assignees, relatedNames); err != nil {
			log.Printf("notifier: failed to notify task %d: %v", task.ID, err)
			continue
		}
	}
}

// loadAssignees batch-resolves every distinct Task.AssignedTo in tasks into
// a userID -> User map, replacing a First() per task. Only users who can
// still receive a reminder are loaded — not soft-deleted (GORM's default
// scope) and still active — so a missing entry means "nobody to email".
func loadAssignees(db *gorm.DB, tasks []models.Task) (map[uint]models.User, error) {
	idSet := make(map[uint]bool)
	for _, t := range tasks {
		if t.AssignedTo != nil {
			idSet[*t.AssignedTo] = true
		}
	}
	if len(idSet) == 0 {
		return nil, nil
	}
	var users []models.User
	if err := db.Where("id IN ? AND is_active = ?", mapKeys(idSet), true).Find(&users).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]models.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	return byID, nil
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

// notifyTaskDue claims the task (see checkDueTasks) and, if this pass won
// the claim and the assignee can still receive it, emails the reminder.
// Returns an error (logged by the caller) if the claim or the send fails.
//
// A task with no assignee, or whose assignee has since been deleted or
// deactivated, or has no email, is claimed without sending: there's nobody
// to remind, and leaving it unclaimed would re-check (and, for a missing
// assignee, log an error about) the same task every 15 minutes forever.
func notifyTaskDue(db *gorm.DB, cfg *config.Config, task models.Task, assignees map[uint]models.User, relatedNames map[relatedKey]string) error {
	claimed, err := claimTask(db, task)
	if err != nil || !claimed {
		return err
	}

	if task.AssignedTo == nil {
		return nil
	}
	assignee, ok := assignees[*task.AssignedTo]
	if !ok || assignee.Email == "" {
		return nil
	}

	subject := fmt.Sprintf("Task due: %s", task.Title)
	body := buildReminderBody(task, relatedNames)

	if err := sendMail(cfg, assignee.Email, subject, body); err != nil {
		return fmt.Errorf("send reminder email (not retried — task already claimed): %w", err)
	}
	return nil
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

// claimTask stamps NotifiedAt only if nobody has yet, reporting whether this
// call did it — the conditional UPDATE is atomic in Postgres, so of two
// instances racing on the same task exactly one sees RowsAffected == 1.
// Also re-checks the task is still pending: one completed since the select
// above shouldn't be reminded about (nor stamped — it's no longer due).
func claimTask(db *gorm.DB, task models.Task) (bool, error) {
	res := db.Model(&models.Task{}).
		Where("id = ? AND notified_at IS NULL AND status = ?", task.ID, models.TaskStatusPending).
		Update("notified_at", gorm.Expr("now()"))
	if res.Error != nil {
		return false, fmt.Errorf("claim task %d: %w", task.ID, res.Error)
	}
	return res.RowsAffected == 1, nil
}
