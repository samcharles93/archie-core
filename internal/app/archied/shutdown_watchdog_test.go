package archied

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// testWatchdogLeash is deliberately short so the watchdog tests are fast and
// deterministic. The real production leash is drain timeout plus grace
// period -- far longer than anything a unit test should wait for.
const testWatchdogLeash = 30 * time.Millisecond

func discardWatchdogLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestShutdownWatchdogHangingPastLeashForcesExit proves the backstop: once
// graceful shutdown begins and never completes (the shutdown machinery is
// hung, so the disarm is never called), the watchdog force-exits the process
// after the leash expires.
func TestShutdownWatchdogHangingPastLeashForcesExit(t *testing.T) {
	exited := make(chan int, 1)
	w := newShutdownWatchdog(testWatchdogLeash, discardWatchdogLogger())
	w.exit = func(code int) { exited <- code }

	began := make(chan struct{})
	_ = w.watch(began)

	// Graceful shutdown begins and never completes: no disarm is called.
	close(began)

	select {
	case code := <-exited:
		if code != 1 {
			t.Fatalf("watchdog force-exited with code %d, want 1", code)
		}
	case <-time.After(testWatchdogLeash * 2):
		t.Fatalf("watchdog did not force-exit within leash %s", testWatchdogLeash)
	}
}

// TestShutdownWatchdogCompletingWithinLeashDoesNotExit proves the inverse:
// a shutdown that completes within the leash (the disarm is called before the
// leash expires) does not trigger the watchdog.
func TestShutdownWatchdogCompletingWithinLeashDoesNotExit(t *testing.T) {
	exited := make(chan int, 1)
	w := newShutdownWatchdog(testWatchdogLeash, discardWatchdogLogger())
	w.exit = func(code int) { exited <- code }

	began := make(chan struct{})
	disarm := w.watch(began)

	// Shutdown begins and completes immediately, well within the leash.
	close(began)
	disarm()

	select {
	case code := <-exited:
		t.Fatalf("watchdog force-exited with code %d despite completing within leash", code)
	case <-time.After(testWatchdogLeash * 3):
		// Correct: a shutdown that completed within the leash did not
		// trigger the forced exit.
	}
}

// TestBootStartShutdownWatchdogDisarmRunsLast pins the wiring invariant that
// makes the backstop sound: the disarm the watchdog registers must be the
// FIRST cleanup so it runs LAST in the LIFO shutdown chain. If it were
// registered after a subsystem's cleanup, a hung subsystem would let the
// disarm run early and the watchdog would stand down before the backstop was
// needed.
func TestBootStartShutdownWatchdogDisarmRunsLast(t *testing.T) {
	b := newBootstrap()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.startShutdownWatchdog(ctx)

	if len(b.cleanups) != 1 {
		t.Fatalf("startShutdownWatchdog registered %d cleanups, want exactly the disarm", len(b.cleanups))
	}

	// A later subsystem's shutdown cleanup is appended after the disarm, so in
	// the LIFO chain it runs BEFORE the disarm.
	var ran []string
	b.addCleanup(func() { ran = append(ran, "subsystem") })
	b.cleanup()

	if len(ran) != 1 || ran[0] != "subsystem" {
		t.Fatalf("cleanup order = %v, want the disarm to run after the subsystem cleanup", ran)
	}
}
