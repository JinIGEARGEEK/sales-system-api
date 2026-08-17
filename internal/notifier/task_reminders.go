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

	for _, task := range tasks {
		if err := notifyTaskDue(db, cfg, task); err != nil {
			log.Printf("notifier: failed to notify task %d: %v", task.ID, err)
			continue
		}
	}
}

// notifyTaskDue resolves the assignee's email, sends the reminder, and marks
// the task as notified. Returns an error (logged by the caller) if the
// assignee can't be resolved or the update fails — a send failure from
// utils.SendMail itself is also surfaced here so the caller logs it and moves
// on to the next task.
func notifyTaskDue(db *gorm.DB, cfg *config.Config, task models.Task) error {
	if task.AssignedTo == nil {
		// Nothing to notify — mark it done so it's not re-checked forever.
		return markNotified(db, task)
	}

	var assignee models.User
	if err := db.First(&assignee, *task.AssignedTo).Error; err != nil {
		return fmt.Errorf("resolve assignee %d: %w", *task.AssignedTo, err)
	}
	if assignee.Email == "" {
		return markNotified(db, task)
	}

	subject := fmt.Sprintf("Task due: %s", task.Title)
	body := buildReminderBody(db, task)

	if err := utils.SendMail(cfg, assignee.Email, subject, body); err != nil {
		return fmt.Errorf("send reminder email: %w", err)
	}

	return markNotified(db, task)
}

// buildReminderBody assembles a simple plain-text email, including the
// related Deal/Contact/Company name when it can be resolved cheaply. A
// failure to resolve the related record just omits that line rather than
// blocking the email.
func buildReminderBody(db *gorm.DB, task models.Task) string {
	body := fmt.Sprintf(
		"Reminder: the following task is due.\n\nTask: %s\nDue date: %s\n",
		task.Title, task.DueDate.Format("2006-01-02 15:04"),
	)

	if related := resolveRelatedName(db, task); related != "" {
		body += fmt.Sprintf("Related %s: %s\n", task.RelatedType, related)
	}

	return body
}

// resolveRelatedName looks up the human-readable name of the Task's related
// Deal/Contact/Company, if any. Returns "" if there's no related record or it
// can't be found — this is a best-effort enrichment, not a hard requirement.
func resolveRelatedName(db *gorm.DB, task models.Task) string {
	if task.RelatedID == 0 {
		return ""
	}

	switch task.RelatedType {
	case models.RelatedTypeDeal:
		var deal models.Deal
		if err := db.First(&deal, task.RelatedID).Error; err == nil {
			return deal.Title
		}
	case models.RelatedTypeContact:
		var contact models.Contact
		if err := db.First(&contact, task.RelatedID).Error; err == nil {
			return contact.Name
		}
	case models.RelatedTypeCompany:
		var company models.Company
		if err := db.First(&company, task.RelatedID).Error; err == nil {
			return company.Name
		}
	}
	return ""
}

func markNotified(db *gorm.DB, task models.Task) error {
	now := time.Now()
	if err := db.Model(&models.Task{}).Where("id = ?", task.ID).Update("notified_at", &now).Error; err != nil {
		return fmt.Errorf("mark task %d notified: %w", task.ID, err)
	}
	return nil
}
