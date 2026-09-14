package utils

import "log"

// SafeGo runs f on its own goroutine with a recover() guard, logging (rather
// than crashing the process on) any panic. Use this instead of a bare `go
// func() { ... }()` for any fire-and-forget background write (an audit-log
// insert, an API key's last_used_at bump, ...) — an unrecovered panic in a
// plain goroutine takes down the entire process, not just the request that
// spawned it, which is a disproportionate failure mode for what's usually a
// best-effort side write that the caller's own response doesn't wait on.
func SafeGo(f func()) {
	SafeGoNotify(f, nil)
}

// SafeGoNotify is SafeGo plus an onPanic callback, invoked with the
// recovered value (after it's logged) — for a caller that needs more than
// "check the logs" visibility into a recovered panic without hand-rolling
// its own recover(). Used by dashboard.go's aggregate fan-out to report
// which aggregate(s) silently failed back to the caller, rather than the
// response simply reporting a wrong-looking zero with no indication
// anything went wrong. onPanic may be nil (that's all SafeGo itself is).
func SafeGoNotify(f func(), onPanic func(recovered any)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("recovered panic in background goroutine: %v", r)
				if onPanic != nil {
					onPanic(r)
				}
			}
		}()
		f()
	}()
}
