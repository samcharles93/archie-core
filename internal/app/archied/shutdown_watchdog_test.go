package archied

import (
	"log/slog"
	"testing"
	"time"
)

// testWatchdogLeash is deliberately short so the watchdog tests are fast and
// deterministic. The real production leash is drain timeout plus grace
// period -- far longer than anything a unit test should wait for.
const testWatchdogLeash = 30 * time.Millisecond

// testWatchdogExitTimeout bounds how long a test waits for the watchdog's
// forced exit to be observed. It is deliberately generous: the leash only
// starts the timer, and the firing goroutine must then be scheduled onto a
// core before the injected exit is observable. A deadline of a few leash
// lengths is a wall-clock race against the Go scheduler, and it fails
// intermittently on a loaded machine -- the test then parks real work behind a
// gate failure that has nothing to do with the change under test. The test
// still completes in ~30ms; this is a ceiling, not a sleep.
const testWatchdogExitTimeout = 5 * time.Second

// testWatchdogStandDownPeriod is the shorter wait used where the test asserts
// the ABSENCE of a forced exit. Load cannot make that assertion lie: a slow
// runner only delays the very exit the test is checking for, so a tight bound
// is safe in this direction and keeps the suite fast. It still leaves the
// leash many times over for a broken disarm to reveal itself, which is the
// regression this half of the pair exists to catch.
const testWatchdogStandDownPeriod = testWatchdogLeash * 10

func discardWatchdogLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestShutdownWatchdogHangingPastLeashForcesExit proves the backstop: once
// graceful shutdown begins and never completes (the shutdown machinery is
// hung, so the disarm is never called), the watchdog force-exits the process
// after the leash expires.
//
// The timing contract asserted here is the one that actually matters and the
// one scheduling jitter cannot falsify: the exit must NOT happen before the
// leash, and it must happen. Waiting for it with a generous ceiling keeps the
// second half honest under load; asserting elapsed >= leash keeps the first
// half strict, because delaying the goroutine only ever makes it later, never
// earlier.
func TestShutdownWatchdogHangingPastLeashForcesExit(t *testing.T) {
	exited := make(chan int, 1)
	w := newShutdownWatchdog(testWatchdogLeash, discardWatchdogLogger())
	w.exit = func(code int) { exited <- code }

	began := make(chan struct{})
	_ = w.watch(began)

	// Graceful shutdown begins and never completes: no disarm is called.
	start := time.Now()
	close(began)

	select {
	case code := <-exited:
		if code != 1 {
			t.Fatalf("watchdog force-exited with code %d, want 1", code)
		}
		if elapsed := time.Since(start); elapsed < testWatchdogLeash {
			t.Fatalf("watchdog force-exited after %s, before the %s leash had expired", elapsed, testWatchdogLeash)
		}
	case <-time.After(testWatchdogExitTimeout):
		t.Fatalf("watchdog did not force-exit within %s (leash %s)", testWatchdogExitTimeout, testWatchdogLeash)
	}
}

// TestShutdownWatchdogCompletingWithinLeashDoesNotExit proves the inverse:
// a shutdown that completes within the leash (the disarm is called before the
// leash expires) does not trigger the watchdog.
//
// The disarm closes the watchdog's done channel, so the goroutine stands down
// and no exit can follow. This asserts the ABSENCE of a forced exit, which is
// the direction load cannot falsify -- a slow runner only delays the exit
// being checked for -- so a tight bound is both safe and honest here.
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
	case <-time.After(testWatchdogStandDownPeriod):
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
	ctx := t.Context()
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
