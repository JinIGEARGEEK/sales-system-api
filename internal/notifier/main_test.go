package notifier

import (
	"os"
	"testing"
	"time"
)

// TestMain runs the package in the production server's zone (Dockerfile
// TZ=Asia/Bangkok), set once before any job goroutine starts — a per-test
// swap of time.Local races with their timers under -race.
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("ICT", 7*3600)
	os.Exit(m.Run())
}
