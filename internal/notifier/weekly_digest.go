// weekly_digest.go — schedules the weekly Overview Pipeline email (package
// digest does the work). Own goroutine, same isolation reasoning as
// forecast_snapshots.go: a failure here can't stall the other jobs.
package notifier

import (
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/digest"
)

const weeklyDigestCheckInterval = time.Hour

// StartWeeklyDigest checks hourly whether this week's digest is due and
// sends it once (digest.MaybeSendWeekly). Safe without SMTP (never sends).
func StartWeeklyDigest(db *gorm.DB, cfg *config.Config) {
	ticker := time.NewTicker(weeklyDigestCheckInterval)
	go func() {
		check := func() {
			if err := digest.MaybeSendWeekly(db, cfg, time.Now()); err != nil {
				log.Printf("weekly digest: %v", err)
			}
		}
		check()
		for range ticker.C {
			check()
		}
	}()
}
