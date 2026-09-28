// jobs.go — the loop every background job runs on: one immediate pass,
// then one per tick until ctx is cancelled, each pass panic-isolated.
package notifier

import (
	"context"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/igeargeek/sales-system-api/internal/utils"
)

// sendMail is what every job here sends email through — a variable so tests
// can capture sends without an SMTP server (same seam as digest.SendMail).
var sendMail = utils.SendMail

// running tracks every job goroutine started by runEvery, for Wait.
var running sync.WaitGroup

// runEvery runs tick once now and then every interval, in its own
// goroutine, until ctx is cancelled (cmd/api/main.go cancels it on
// SIGTERM). Cancellation stops the ticker and lets an in-flight pass finish
// rather than aborting it midway — a pass that dies between claiming a row
// and acting on it is exactly the half-done state the claim/log schemes
// here try to avoid.
func runEvery(ctx context.Context, name string, interval time.Duration, tick func()) {
	running.Add(1)
	go func() {
		defer running.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			safeTick(name, tick)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// safeTick runs one pass, turning a panic into a log line. Without it a
// panic in any pass (a nil pointer on an unexpected row, say) crashed the
// whole process — every in-flight HTTP request with it — and a panic in
// just this goroutine can't be caught anywhere else. The job carries on at
// its next tick.
func safeTick(name string, tick func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("notifier: %s pass panicked (will retry next tick): %v\n%s", name, r, debug.Stack())
		}
	}()
	tick()
}

// Wait blocks until every job goroutine has returned (after the ctx passed
// to the Start* functions is cancelled) or timeout elapses, reporting
// whether they all finished. main calls it on shutdown before closing the
// database, so a pass isn't cut off mid-query by the pool closing under it.
func Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		running.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
