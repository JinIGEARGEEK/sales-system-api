package utils

import (
	"fmt"
	"log"
	"net/smtp"

	"github.com/igeargeek/sales-system-api/internal/config"
)

// SendMail sends a plain-text email using the standard library's net/smtp with
// STARTTLS-capable PLAIN auth (works with providers like SendGrid, Mailgun,
// Gmail app passwords, etc. over port 587).
//
// If cfg.SMTPHost is empty (the default — no SMTP configured), this logs a
// warning and returns nil instead of erroring, so features built on top of
// this (e.g. the Task due-date reminder ticker) are safe to deploy before
// real SMTP credentials exist.
func SendMail(cfg *config.Config, to, subject, body string) error {
	if cfg == nil || cfg.SMTPHost == "" {
		log.Printf("mailer: SMTP_HOST not configured — skipping email to %s (subject: %q)", to, subject)
		return nil
	}
	if to == "" {
		log.Printf("mailer: no recipient address — skipping email (subject: %q)", subject)
		return nil
	}

	addr := fmt.Sprintf("%s:%s", cfg.SMTPHost, cfg.SMTPPort)

	var auth smtp.Auth
	if cfg.SMTPUsername != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)
	}

	from := cfg.SMTPFrom
	if from == "" {
		from = cfg.SMTPUsername
	}

	msg := []byte(
		"From: " + from + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"\r\n" +
			body + "\r\n",
	)

	if err := smtp.SendMail(addr, auth, from, []string{to}, msg); err != nil {
		return fmt.Errorf("send mail to %s: %w", to, err)
	}
	return nil
}
