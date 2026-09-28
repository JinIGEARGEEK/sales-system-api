package apitests

import (
	"os"
	"testing"
	"time"
)

// ictZone is the production server's zone (Dockerfile TZ=Asia/Bangkok, no
// DST), fixed so these tests don't depend on the machine running them.
var ictZone = time.FixedZone("ICT", 7*3600)

// TestMain runs the package in the production server's zone (Dockerfile
// TZ=Asia/Bangkok) whatever the machine's own TZ, so the local-day tests
// (date ranges, quote validity, month buckets) mean the same thing locally
// and in CI. Set once here rather than swapped per test: the app's timers
// read time.Local from their own goroutines, so a per-test swap is a data
// race under -race.
func TestMain(m *testing.M) {
	time.Local = ictZone
	os.Exit(m.Run())
}
