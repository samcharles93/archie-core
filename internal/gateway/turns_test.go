package gateway_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/gateway"
)

func testTurns(t *testing.T) *gateway.Turns {
	t.Helper()
	return gateway.NewTurns(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestSubmitDoesNotBlockCaller is the whole reason this type exists: the
// channel event loop must stay free while a turn runs, or the command that
// stops the turn can never be delivered.
func TestSubmitDoesNotBlockCaller(t *testing.T) {
	t.Parallel()

	turns := testTurns(t)
	release := make(chan struct{})
	started := make(chan struct{})

	turns.Submit(context.Background(), "s1", func(context.Context) {
		close(started)
		<-release
	})
	<-started
	defer close(release)

	returned := make(chan struct{})
	go func() {
		turns.Submit(context.Background(), "s1", func(context.Context) {})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Submit blocked while a turn was running; the event loop would be stuck")
	}
}

func TestStopCancelsRunningTurn(t *testing.T) {
	t.Parallel()

	turns := testTurns(t)
	started := make(chan struct{})
	var cause atomic.Value

	turns.Submit(context.Background(), "s1", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		cause.Store(ctx.Err())
	})
	<-started

	cancelled, dropped := turns.Stop("s1")
	if !cancelled {
		t.Error("Stop reported no running turn")
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}

	waitFor(t, func() bool { return cause.Load() != nil })
	if err, _ := cause.Load().(error); !errors.Is(err, context.Canceled) {
		t.Errorf("turn context error = %v, want context.Canceled", err)
	}
}

// TestStopDropsQueuedTurns guards the "stop means stop" rule. If the
// queued turn ran after cancellation, the sender would see a new reply
// begin immediately and conclude the stop had failed.
func TestStopDropsQueuedTurns(t *testing.T) {
	t.Parallel()

	turns := testTurns(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var queuedRan atomic.Bool

	turns.Submit(context.Background(), "s1", func(context.Context) {
		close(started)
		<-release
	})
	<-started

	for range 2 {
		turns.Submit(context.Background(), "s1", func(context.Context) { queuedRan.Store(true) })
	}

	cancelled, dropped := turns.Stop("s1")
	if !cancelled || dropped != 2 {
		t.Fatalf("Stop() = (%v, %d), want (true, 2)", cancelled, dropped)
	}
	close(release)

	// Give the lane time to pull anything that survived the drain.
	time.Sleep(100 * time.Millisecond)
	if queuedRan.Load() {
		t.Error("a queued turn ran after /stop")
	}
}

// TestStopLeavesTheCallersContextAlone checks that Stop only reaches the
// contexts Turns derived. Callers pass in the channel's long-lived run
// context, so cancelling that instead of the turn's own would take the
// whole gateway down every time someone sent /stop.
func TestStopLeavesTheCallersContextAlone(t *testing.T) {
	t.Parallel()

	turns := testTurns(t)
	callerCtx, callerCancel := context.WithCancel(context.Background())
	defer callerCancel()

	started := make(chan struct{})
	turns.Submit(callerCtx, "s1", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	})
	<-started
	turns.Submit(callerCtx, "s1", func(context.Context) {})

	if cancelled, dropped := turns.Stop("s1"); !cancelled || dropped != 1 {
		t.Fatalf("Stop() = (%v, %d), want (true, 1)", cancelled, dropped)
	}
	if err := callerCtx.Err(); err != nil {
		t.Errorf("Stop cancelled the caller's context: %v", err)
	}

	// The lane must still be usable afterwards.
	ran := make(chan struct{})
	turns.Submit(callerCtx, "s1", func(context.Context) { close(ran) })
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("the session stopped accepting turns after /stop")
	}
}

// TestDeepBacklogIsKeptAndOrdered is the no-shedding contract. A message
// that is silently dropped surfaces much later as an agent that ignored
// something, which is far more expensive to diagnose than holding the
// backlog. Every message queues, and they run in the order they arrived.
func TestDeepBacklogIsKeptAndOrdered(t *testing.T) {
	t.Parallel()

	const backlog = 100

	turns := testTurns(t)
	started := make(chan struct{})
	release := make(chan struct{})

	turns.Submit(context.Background(), "s1", func(context.Context) {
		close(started)
		<-release
	})
	<-started

	var mu sync.Mutex
	var order []int
	done := make(chan struct{}, backlog)
	for i := range backlog {
		turns.Submit(context.Background(), "s1", func(context.Context) {
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			done <- struct{}{}
		})
	}

	if got := turns.Queued("s1"); got != backlog {
		t.Fatalf("Queued() = %d, want all %d held", got, backlog)
	}
	close(release)

	for range backlog {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("queued turns did not all run")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != backlog {
		t.Fatalf("ran %d turns, want %d", len(order), backlog)
	}
	for i, got := range order {
		if got != i {
			t.Fatalf("turn %d ran at position %d; the queue is not FIFO", got, i)
		}
	}
}

// TestTurnsRunSeriallyWithinSession covers the queueing half of the
// contract: replies must not interleave for one conversation.
func TestTurnsRunSeriallyWithinSession(t *testing.T) {
	t.Parallel()

	turns := testTurns(t)
	var mu sync.Mutex
	var concurrent, peak int
	done := make(chan struct{}, 3)

	for range 3 {
		turns.Submit(context.Background(), "s1", func(context.Context) {
			mu.Lock()
			concurrent++
			if concurrent > peak {
				peak = concurrent
			}
			mu.Unlock()

			time.Sleep(20 * time.Millisecond)

			mu.Lock()
			concurrent--
			mu.Unlock()
			done <- struct{}{}
		})
	}
	for range 3 {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("turns did not complete")
		}
	}
	if peak != 1 {
		t.Errorf("peak concurrency = %d, want 1; turns in one session overlapped", peak)
	}
}

// TestSessionsAreIndependent checks that one busy session cannot stall
// another, and that Stop is scoped to the session that asked for it.
func TestSessionsAreIndependent(t *testing.T) {
	t.Parallel()

	turns := testTurns(t)
	busy := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	turns.Submit(context.Background(), "busy", func(context.Context) {
		close(busy)
		<-release
	})
	<-busy

	ran := make(chan struct{})
	turns.Submit(context.Background(), "other", func(context.Context) { close(ran) })
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("a second session was blocked behind the first")
	}

	if cancelled, _ := turns.Stop("other"); cancelled {
		t.Error("Stop cancelled a turn in an idle session")
	}
}

// TestStopAfterCompletionIsInert guards the retraction in runOne: a stale
// cancel left behind by a finished turn would kill the next one.
func TestStopAfterCompletionIsInert(t *testing.T) {
	t.Parallel()

	turns := testTurns(t)
	done := make(chan struct{})
	turns.Submit(context.Background(), "s1", func(context.Context) { close(done) })
	<-done
	waitFor(t, func() bool { return !turns.Running("s1") })

	if cancelled, dropped := turns.Stop("s1"); cancelled || dropped != 0 {
		t.Errorf("Stop() = (%v, %d) on an idle session, want (false, 0)", cancelled, dropped)
	}
}

// TestPanicInTurnDoesNotKillLane covers the recovery in runOne. Losing the
// lane goroutine would leave the session permanently unable to reply.
func TestPanicInTurnDoesNotKillLane(t *testing.T) {
	t.Parallel()

	turns := testTurns(t)
	turns.Submit(context.Background(), "s1", func(context.Context) { panic("boom") })

	ran := make(chan struct{})
	turns.Submit(context.Background(), "s1", func(context.Context) { close(ran) })
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("the lane died with the panicking turn")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
