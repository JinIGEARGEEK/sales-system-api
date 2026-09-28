package handlers

import (
	"os"
	"testing"
	"time"
)

// TestMain pins time.Local to Bangkok's +07:00 (the Dockerfile's TZ).
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("ICT", 7*3600)
	os.Exit(m.Run())
}
