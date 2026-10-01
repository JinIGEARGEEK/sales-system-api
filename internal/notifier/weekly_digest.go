// weekly_digest.go — schedules the weekly Overview Pipeline email (package
// digest does the work). Own goroutine, same isolation reasoning as
// forecast_snapshots.go: a failure here can't stall the other jobs.
package notifier

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/digest"
)

const weeklyDigestCheckInterval = time.Hour

// StartWeeklyDigest checks hourly whether this week's digest is due and
// sends it once (digest.MaybeSendWeekly). Safe without SMTP (never sends).
// Stops when ctx is cancelled (see runEvery).
func StartWeeklyDigest(ctx context.Context, db *gorm.DB, cfg *config.Config) {
	runEvery(ctx, "weekly digest", weeklyDigestCheckInterval, func() {
		if err := digest.MaybeSendWeekly(db, cfg, time.Now()); err != nil {
			log.Printf("weekly digest: %v", err)
		}
	})
}
