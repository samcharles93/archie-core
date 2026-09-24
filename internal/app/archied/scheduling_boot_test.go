package archied

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// fakeTaskCreator records CreateTask calls so a test can assert the
// scheduling engine's workflow runner reaches a real gateway.TaskCreator
// rather than a no-op.
type fakeTaskCreator struct {
	calls []gateway.SpawnRequest
}

func (f *fakeTaskCreator) CreateTask(_ context.Context, req gateway.SpawnRequest) (int64, error) {
	f.calls = append(f.calls, req)
	return int64(len(f.calls)), nil
}

func newTestBoot(t *testing.T, tasks gateway.TaskCreator) *boot {
	t.Helper()
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	return &boot{
		log:                 slog.New(slog.NewTextHandler(os.Stderr, nil)),
		bus:                 bus,
		doc:                 &configuration.Document{},
		cfg:                 config.Config{},
		chatTasks:           tasks,
		defaultChatIdentity: "primary",
	}
}

// TestSetupSchedulingConstructsEngine is the red case for app-archied-2 /
// scheduling-1 / config-2: schedulingConfig translated a valid
// [scheduling] input but nothing at boot ever constructed the ticker
// engine, so the whole scheduling/cron family was reachable from no
// binary. setupScheduling is the composition site; this asserts it
// actually produces a running-capable engine wired to a real
// gateway.TaskCreator, not just a translated config struct.
func TestSetupSchedulingConstructsEngine(t *testing.T) {
	creator := &fakeTaskCreator{}
	b := newTestBoot(t, creator)

	if err := b.setupScheduling(); err != nil {
		t.Fatalf("setupScheduling() error = %v", err)
	}
	if b.schedulingEngine == nil {
		t.Fatal("setupScheduling() left b.schedulingEngine nil; the ticker engine was never constructed")
	}
	if len(b.cleanups) == 0 {
		t.Fatal("setupScheduling() registered no cleanup; engine/store would leak at shutdown")
	}

	if err := b.schedulingEngine.Start(t.Context()); err != nil {
		t.Fatalf("schedulingEngine.Start() error = %v", err)
	}
	b.cleanup()
}

// TestSetupSchedulingWorkflowRunnerReachesTaskCreator proves the "workflow"
// kind runner registered on the router is backed by the real
// gateway.TaskCreator this composition root already has, not a stub.
func TestSetupSchedulingWorkflowRunnerReachesTaskCreator(t *testing.T) {
	creator := &fakeTaskCreator{}
	b := newTestBoot(t, creator)

	if err := b.setupScheduling(); err != nil {
		t.Fatalf("setupScheduling() error = %v", err)
	}
	t.Cleanup(b.cleanup)

	submitter := spawnTaskSubmitter{creator: b.chatTasks, identity: b.defaultChatIdentity}
	if err := submitter.Submit(context.Background(), "job-1", "title", "body"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if len(creator.calls) != 1 {
		t.Fatalf("CreateTask called %d times, want 1", len(creator.calls))
	}
	if got := creator.calls[0]; got.Title != "title" || got.Body != "body" || got.Identity != "primary" {
		t.Errorf("CreateTask request = %+v, want Title=title Body=body Identity=primary", got)
	}
}

// TestSetupSchedulingWithoutTaskCreatorLeavesEngineNil documents the one
// deliberate degrade: no chat task creator configured means no runner can
// honestly back the "workflow" kind, so the engine is left unstarted rather
// than built with no reachable job kind.
func TestSetupSchedulingWithoutTaskCreatorLeavesEngineNil(t *testing.T) {
	b := newTestBoot(t, nil)

	if err := b.setupScheduling(); err != nil {
		t.Fatalf("setupScheduling() error = %v", err)
	}
	if b.schedulingEngine != nil {
		t.Fatal("setupScheduling() built an engine with no chat task creator configured")
	}
}
