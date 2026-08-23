package agentworker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/store"
)

type workerPublisher struct{ messages *[][]byte }

func (p workerPublisher) Publish(_ string, data []byte) error {
	if p.messages != nil {
		*p.messages = append(*p.messages, append([]byte(nil), data...))
	}
	return nil
}

type workerSubscription struct {
	close    func()
	closeErr error
}

func (s workerSubscription) Close() error {
	s.close()
	return s.closeErr
}

type workerTransportStub struct {
	events         *[]string
	subscribeErr   error
	subCloseErr    error
	publisher      agentexec.LogPublisher
	eventPublisher agentexec.EventPublisher
	dedicated      bool
}

func (*workerTransportStub) FetchStage(context.Context) (agentexec.StageMessage, error) {
	return nil, nil
}
func (t *workerTransportStub) Close() { *t.events = append(*t.events, "transport-close") }
func (t *workerTransportStub) LogPublisher() agentexec.LogPublisher {
	*t.events = append(*t.events, "log-publisher")
	if t.publisher != nil {
		return t.publisher
	}
	return workerPublisher{}
}

func (t *workerTransportStub) EventPublisher() agentexec.EventPublisher {
	*t.events = append(*t.events, "event-publisher")
	if t.eventPublisher != nil {
		return t.eventPublisher
	}
	return workerPublisher{}
}

func (t *workerTransportStub) SubscribeTasks(_ context.Context, _ int64, dedicated bool, _ agentnats.TaskHandler, _ *slog.Logger) (agentnats.Subscription, error) {
	*t.events = append(*t.events, "subscribe")
	t.dedicated = dedicated
	if t.subscribeErr != nil {
		return nil, t.subscribeErr
	}
	return workerSubscription{
		close:    func() { *t.events = append(*t.events, "subscription-close") },
		closeErr: t.subCloseErr,
	}, nil
}

func (*workerTransportStub) Forger(string, time.Duration) workflow.Forger { return nil }

func (*workerTransportStub) Store(time.Duration) store.WorkflowStore { return nil }

func (*workerTransportStub) Trees(string, string, time.Duration) agentnats.RemoteTrees { return nil }

func TestRunPassesCompleteTransportConfiguration(t *testing.T) {
	var got agentnats.Config
	var events []string
	transport := &workerTransportStub{events: &events}
	ctx, cancel := context.WithCancel(t.Context())
	dependencies := workerDependencies{
		markSafe: func(context.Context, string, *slog.Logger) bool { return true },
		connect: func(_ context.Context, config agentnats.Config, _ *slog.Logger) (workerTransport, error) {
			got = config
			return transport, nil
		},
		bootID:  func(string) (int64, bool, error) { return 0, false, nil },
		runLoop: func(context.Context, stageBus, *slog.Logger) int { cancel(); return 0 },
	}
	settings := Settings{NATSURL: "nats://test", NATSToken: "secret", Consumer: "worker", WorkDir: "/worktree"}
	if err := run(ctx, settings, slog.New(slog.DiscardHandler), dependencies); err != nil {
		t.Fatal(err)
	}
	if got.URL != settings.NATSURL || got.Token != settings.NATSToken || got.ConsumerName != settings.Consumer {
		t.Fatalf("connection config = %#v, want URL/token/consumer from settings", got)
	}
}

func TestRunPreservesStartupAndReverseCleanupOrder(t *testing.T) {
	var events []string
	transport := &workerTransportStub{events: &events}
	dependencies := workerDependencies{
		markSafe: func(context.Context, string, *slog.Logger) bool {
			events = append(events, "git-safety")
			return true
		},
		connect: func(context.Context, agentnats.Config, *slog.Logger) (workerTransport, error) {
			events = append(events, "connect")
			return transport, nil
		},
		bootID: func(string) (int64, bool, error) {
			events = append(events, "boot-id")
			return 42, true, nil
		},
		runLoop: func(context.Context, stageBus, *slog.Logger) int {
			events = append(events, "stage-loop")
			return 0
		},
	}
	if err := run(t.Context(), Settings{NATSURL: "nats://test", Consumer: "worker", WorkDir: "/worktree"}, slog.New(slog.DiscardHandler), dependencies); err != nil {
		t.Fatal(err)
	}
	want := []string{"git-safety", "connect", "boot-id", "log-publisher", "subscribe", "stage-loop", "subscription-close", "transport-close"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
	if !transport.dedicated {
		t.Fatal("dedicated boot ID did not select dedicated subscription")
	}
}

func TestRunWarnsWhenNormalSubscriptionCleanupFails(t *testing.T) {
	closeErr := errors.New("unsubscribe failed")
	var events []string
	transport := &workerTransportStub{events: &events, subCloseErr: closeErr}
	dependencies := workerDependencies{
		markSafe: func(context.Context, string, *slog.Logger) bool { return true },
		connect: func(context.Context, agentnats.Config, *slog.Logger) (workerTransport, error) {
			return transport, nil
		},
		bootID:  func(string) (int64, bool, error) { return 0, false, nil },
		runLoop: func(context.Context, stageBus, *slog.Logger) int { return 0 },
	}
	var output bytes.Buffer
	if err := run(t.Context(), Settings{NATSURL: "nats://test", Consumer: "worker"}, slog.New(slog.NewTextHandler(&output, nil)), dependencies); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "task run unsubscribe failed") || !strings.Contains(output.String(), closeErr.Error()) {
		t.Fatalf("cleanup log = %q, want warning containing %q", output.String(), closeErr)
	}
}

func TestRunStartupFailuresCleanUpOwnedResources(t *testing.T) {
	connectErr := errors.New("connect failed")
	bootErr := errors.New("invalid boot metadata")
	subscribeErr := errors.New("subscribe failed")
	tests := []struct {
		name          string
		connectErr    error
		bootErr       error
		subscribeErr  error
		dedicated     bool
		wantErr       error
		wantOperation string
		wantClosed    bool
	}{
		{name: "connect", connectErr: connectErr, wantErr: connectErr, wantOperation: "nats connect failed"},
		{name: "core connection", connectErr: fmt.Errorf("%w: unavailable", agentnats.ErrCoreConnectionUnavailable), wantErr: agentnats.ErrCoreConnectionUnavailable, wantOperation: "nats connection unavailable"},
		{name: "boot metadata", bootErr: bootErr, wantErr: bootErr, wantOperation: "task boot failed", wantClosed: true},
		{name: "subscribe", subscribeErr: subscribeErr, dedicated: true, wantErr: subscribeErr, wantOperation: "taskrun subscribe failed", wantClosed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var events []string
			var published [][]byte
			transport := &workerTransportStub{events: &events, subscribeErr: test.subscribeErr, publisher: workerPublisher{messages: &published}}
			dependencies := workerDependencies{
				markSafe: func(context.Context, string, *slog.Logger) bool { return true },
				connect: func(context.Context, agentnats.Config, *slog.Logger) (workerTransport, error) {
					return transport, test.connectErr
				},
				bootID: func(string) (int64, bool, error) {
					if test.bootErr != nil {
						return 0, false, test.bootErr
					}
					if test.dedicated {
						return 42, true, nil
					}
					return 0, false, nil
				},
				runLoop: func(context.Context, stageBus, *slog.Logger) int { t.Fatal("stage loop called"); return 0 },
			}
			var output bytes.Buffer
			err := run(t.Context(), Settings{NATSURL: "nats://test", Consumer: "worker"}, slog.New(slog.NewTextHandler(&output, nil)), dependencies)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			var startupErr *StartupError
			if !errors.As(err, &startupErr) || startupErr.Operation != test.wantOperation {
				t.Fatalf("startup error = %#v, want operation %q", startupErr, test.wantOperation)
			}
			closed := len(events) > 0 && events[len(events)-1] == "transport-close"
			if closed != test.wantClosed {
				t.Fatalf("transport closed = %v, want %v; events %v", closed, test.wantClosed, events)
			}
			if test.bootErr != nil {
				for _, event := range events {
					if event == "subscribe" {
						t.Fatalf("invalid boot metadata reached task subscription: events %v", events)
					}
				}
			}
			if got := strings.Count(output.String(), test.wantOperation); got != 1 {
				t.Fatalf("operation log count = %d, want 1; output %q", got, output.String())
			}
			if test.subscribeErr != nil {
				var joined strings.Builder
				for _, data := range published {
					joined.WriteString(string(data))
				}
				if !strings.Contains(joined.String(), test.wantOperation) {
					t.Fatalf("task-scoped logs %q do not contain %q", joined.String(), test.wantOperation)
				}
			}
		})
	}
}
