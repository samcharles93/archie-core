package builtin

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunWithContext_ReturnsFnResult(t *testing.T) {
	err := runWithContext(context.Background(), func() error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sentinel := errors.New("boom")
	err = runWithContext(context.Background(), func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// TestRunWithContext_ReturnsCtxErrOnAlreadyExpired verifies that when the
// context is already done before runWithContext is called, no goroutine is
// spawned and the context error is returned immediately.
func TestRunWithContext_ReturnsCtxErrOnAlreadyExpired(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	spawned := make(chan struct{})
	err := runWithContext(ctx, func() error {
		close(spawned)
		return nil
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}

	// The goroutine must NOT have been spawned because the context
	// was already expired. Give it a short grace period to prove the
	// negative.
	select {
	case <-spawned:
		t.Fatal("fn was spawned even though the context was already expired — goroutine leak")
	case <-time.After(50 * time.Millisecond):
		// Expected: fn was never called.
	}
}

// TestRunWithContext_ReturnsCtxErrOnTimeout is the core regression coverage
// for tau-6wa: fn blocks forever (simulating a hung filesystem call); the
// caller must get control back once ctx expires rather than waiting for fn.
func TestRunWithContext_ReturnsCtxErrOnTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	fnStarted := make(chan struct{})
	blockForever := make(chan struct{}) // deliberately never closed

	start := time.Now()
	err := runWithContext(ctx, func() error {
		close(fnStarted)
		<-blockForever
		return nil
	})
	elapsed := time.Since(start)

	<-fnStarted // make sure fn actually started (and is now hung) before checking the result

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("runWithContext took %v to return after the deadline, expected it to return promptly instead of waiting on the hung fn", elapsed)
	}
}
