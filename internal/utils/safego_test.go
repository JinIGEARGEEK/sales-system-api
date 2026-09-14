package utils

import (
	"sync"
	"testing"
	"time"
)

// TestSafeGo_RecoversPanicWithoutCrashing guards SafeGo's core promise: a
// panic inside f is recovered, not left to crash the process. There's
// nothing to assert on directly (a crashed process can't report a test
// failure) beyond "the test itself is still running after this" and that
// execution reaches past the panic.
func TestSafeGo_RecoversPanicWithoutCrashing(t *testing.T) {
	done := make(chan struct{})
	SafeGo(func() {
		defer close(done)
		panic("boom")
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never reached its deferred close(done) — panic wasn't recovered as expected")
	}
}

// TestSafeGoNotify_InvokesOnPanicWithRecoveredValue guards the callback
// dashboard.go's aggregate fan-out relies on to populate its own
// degraded_aggregates response field: onPanic must fire, exactly once, with
// the value passed to panic().
func TestSafeGoNotify_InvokesOnPanicWithRecoveredValue(t *testing.T) {
	var mu sync.Mutex
	var got []any

	done := make(chan struct{})
	SafeGoNotify(func() {
		panic("aggregate exploded")
	}, func(r any) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
		close(done)
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onPanic was never called")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("onPanic called %d times, want 1", len(got))
	}
	if got[0] != "aggregate exploded" {
		t.Fatalf("onPanic recovered value = %v, want %q", got[0], "aggregate exploded")
	}
}

// TestSafeGoNotify_NoPanicNeverCallsOnPanic guards the non-failure path:
// onPanic must NOT fire when f completes normally.
func TestSafeGoNotify_NoPanicNeverCallsOnPanic(t *testing.T) {
	done := make(chan struct{})
	called := false
	SafeGoNotify(func() {
		close(done)
	}, func(r any) {
		called = true
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("f never ran")
	}
	// Give a moment for a (wrongly-firing) onPanic to run, since f's own
	// close(done) races the deferred recover() block's own completion.
	time.Sleep(20 * time.Millisecond)
	if called {
		t.Fatal("onPanic was called even though f did not panic")
	}
}
