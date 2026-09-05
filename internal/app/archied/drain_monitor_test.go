package archied

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/drain"
)

const testInterval = 5 * time.Millisecond

// countShutdown returns a shutdown func plus a channel signalled on each call.
func countShutdown(t *testing.T) (func(), *chan struct{}) {
	t.Helper()
	calls := make(chan struct{}, 1)
	return func() {
		select {
		case calls <- struct{}{}:
		default:
		}
	}, &calls
}

func TestMonitorTriggersShutdownOnValidDecision(t *testing.T) {
	shutdown, calls := countShutdown(t)
	err := monitorDrainRequests(
		context.Background(),
		func() (drain.Decision, error) { return drain.DecisionValid, nil },
		shutdown,
		slog.New(slog.DiscardHandler),
		testInterval,
	)
	if err != nil {
		t.Fatalf("monitorDrainRequests() error = %v, want nil", err)
	}
	select {
	case <-*calls:
	default:
		t.Fatal("shutdown was not invoked on a valid drain decision")
	}
}

func TestMonitorDoesNotShutdownOnStaleDecision(t *testing.T) {
	shutdown, calls := countShutdown(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// First poll returns stale; the monitor waits one interval. Cancel the
	// context before the next poll, then assert shutdown was never invoked and
	// the monitor returned the cancellation.
	go func() {
		time.Sleep(testInterval * 3)
		cancel()
	}()

	err := monitorDrainRequests(
		ctx,
		func() (drain.Decision, error) { return drain.DecisionStale, nil },
		shutdown,
		slog.New(slog.DiscardHandler),
		testInterval,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("monitorDrainRequests() error = %v, want context.Canceled", err)
	}
	select {
	case <-*calls:
		t.Fatal("shutdown was invoked on a stale drain decision")
	default:
	}
}

func TestMonitorDoesNotShutdownOnNoMarker(t *testing.T) {
	shutdown, calls := countShutdown(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(testInterval * 3)
		cancel()
	}()

	err := monitorDrainRequests(
		ctx,
		func() (drain.Decision, error) { return drain.DecisionNone, nil },
		shutdown,
		slog.New(slog.DiscardHandler),
		testInterval,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("monitorDrainRequests() error = %v, want context.Canceled", err)
	}
	select {
	case <-*calls:
		t.Fatal("shutdown was invoked with no drain marker")
	default:
	}
}

func TestMonitorDoesNotShutdownOnCheckError(t *testing.T) {
	shutdown, calls := countShutdown(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(testInterval * 3)
		cancel()
	}()

	boom := errors.New("marker unreadable")
	err := monitorDrainRequests(
		ctx,
		func() (drain.Decision, error) { return drain.DecisionStale, boom },
		shutdown,
		slog.New(slog.DiscardHandler),
		testInterval,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("monitorDrainRequests() error = %v, want context.Canceled", err)
	}
	select {
	case <-*calls:
		t.Fatal("shutdown was invoked despite a check error")
	default:
	}
}

func TestMonitorShutdownAfterStaleThenValid(t *testing.T) {
	shutdown, calls := countShutdown(t)
	callsMade := 0
	err := monitorDrainRequests(
		context.Background(),
		func() (drain.Decision, error) {
			callsMade++
			if callsMade == 1 {
				return drain.DecisionStale, nil
			}
			return drain.DecisionValid, nil
		},
		shutdown,
		slog.New(slog.DiscardHandler),
		testInterval,
	)
	if err != nil {
		t.Fatalf("monitorDrainRequests() error = %v, want nil", err)
	}
	select {
	case <-*calls:
	default:
		t.Fatal("shutdown was not invoked after a marker became valid")
	}
}
