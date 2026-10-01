package utils

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/igeargeek/sales-system-api/internal/config"
)

// captureLog redirects the standard logger for the duration of fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	defer func() { log.SetOutput(prev); log.SetFlags(prevFlags) }()
	fn()
	return buf.String()
}

// Without SMTP_HOST, SendMail must neither error nor log: it runs once per
// recipient per background tick, and "email is off" is a permanent,
// expected state here (LogMailStatus reports it once at startup).
func TestSendMail_NoSMTPIsSilentNoOp(t *testing.T) {
	for _, cfg := range []*config.Config{nil, {SMTPHost: ""}} {
		out := captureLog(t, func() {
			for i := 0; i < 3; i++ {
				if err := SendMail(cfg, "rep@example.com", "Deal idle", "body"); err != nil {
					t.Fatalf("SendMail without SMTP returned %v, want nil", err)
				}
			}
		})
		if out != "" {
			t.Errorf("SendMail without SMTP logged %q, want nothing", out)
		}
	}
}

func TestMailEnabledAndLogMailStatus(t *testing.T) {
	if MailEnabled(nil) || MailEnabled(&config.Config{}) {
		t.Error("MailEnabled must be false without SMTP_HOST")
	}
	if !MailEnabled(&config.Config{SMTPHost: "smtp.example.com"}) {
		t.Error("MailEnabled must be true with SMTP_HOST set")
	}
	out := captureLog(t, func() { LogMailStatus(&config.Config{}) })
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "email is disabled") {
		t.Errorf("LogMailStatus without SMTP logged %q, want one 'email is disabled' line", out)
	}
}
