package utils

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"

	"github.com/igeargeek/sales-system-api/internal/config"
)

// SendMail sends a plain-text email over an explicitly-verified TLS
// connection with PLAIN auth (works with providers like SendGrid, Mailgun,
// Gmail app passwords, etc. over port 587/STARTTLS or 465/implicit TLS).
//
// Deliberately does NOT use the stdlib's net/smtp.SendMail convenience
// function: it only *opportunistically* upgrades to STARTTLS if the server
// offers it, with no explicit tls.Config and — critically — no failure if
// the server doesn't offer it at all, so a misconfigured or
// downgrade-attacked relay would silently receive PLAIN-auth credentials and
// the message body in cleartext. This dials TLS itself (port 465) or
// requires and verifies STARTTLS (any other port, e.g. 587) and returns an
// error rather than falling back to an unencrypted connection either way.
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

	client, err := dialSMTP(cfg)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer func() { _ = client.Close() }()

	if cfg.SMTPUsername != "" {
		auth := smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO %s: %w", to, err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write mail body to %s: %w", to, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("send mail to %s: %w", to, err)
	}
	return client.Quit()
}

// dialSMTP connects and, for any port other than the well-known implicit-TLS
// port 465, requires and verifies STARTTLS — returning an error (never a
// plaintext connection) if the server doesn't offer it.
func dialSMTP(cfg *config.Config) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.SMTPHost, cfg.SMTPPort)
	tlsConfig := &tls.Config{ServerName: cfg.SMTPHost}

	if cfg.SMTPPort == "465" {
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return nil, err
		}
		return smtp.NewClient(conn, cfg.SMTPHost)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		_ = client.Close()
		return nil, fmt.Errorf("SMTP server at %s does not offer STARTTLS — refusing to send credentials/mail in cleartext", addr)
	}
	if err := client.StartTLS(tlsConfig); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("STARTTLS handshake: %w", err)
	}
	return client, nil
}
