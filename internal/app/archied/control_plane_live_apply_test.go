package archied

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// The limits this package's fixtures boot or run with, and the documents the
// control plane stores them as. Named so a test that asserts "the running
// limits stayed" and a test that asserts "the update was applied" cannot
// disagree about what "running" means.
var (
	bootedSettings = workflow.ExecutionSettings{MaxModelToolSteps: 10, MaxRuntime: 5 * time.Minute, MaxConsecutiveGateFailures: 3}
	bootedDocument = `{"max_model_tool_steps": 10, "max_runtime_seconds": 300, "max_consecutive_gate_failures": 3}`
)

// applyStatusRecorder is the State Store end of the apply-status surface: it
// keeps what a process reported, so a test can assert what the settings page
// would render.
type applyStatusRecorder struct {
	mu     sync.Mutex
	writes []storecontract.ApplyStatus
}

func (r *applyStatusRecorder) PutApplyStatus(_ context.Context, status storecontract.ApplyStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, status)
	return nil
}

func (r *applyStatusRecorder) ListApplyStatus(context.Context) ([]storecontract.ApplyStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]storecontract.ApplyStatus(nil), r.writes...), nil
}

// last returns the most recent record written for kind.
func (r *applyStatusRecorder) last(kind string) (storecontract.ApplyStatus, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.writes) - 1; i >= 0; i-- {
		if r.writes[i].Kind == kind {
			return r.writes[i], true
		}
	}
	return storecontract.ApplyStatus{}, false
}

// awaitCount waits until n records have been written for kind. The live apply
// runs on the watch goroutine, so a test that drives it through the stream has
// nothing to synchronise on but the record it is waiting for.
func (r *applyStatusRecorder) awaitCount(t *testing.T, kind string, n int) []storecontract.ApplyStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		written := make([]storecontract.ApplyStatus, 0, n)
		for _, status := range r.writes {
			if status.Kind == kind {
				written = append(written, status)
			}
		}
		r.mu.Unlock()
		if len(written) >= n {
			return written
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d records reported, want %d", kind, len(written), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// newLiveApplyBoot builds a boot running the given snapshot, whose apply status
// reports land in a recorder.
func newLiveApplyBoot(t *testing.T, running config.Config) (*boot, *applyStatusRecorder) {
	t.Helper()
	recorder := &applyStatusRecorder{}
	b := &boot{
		cfg:         running,
		log:         slog.New(slog.DiscardHandler),
		cfgHolder:   config.NewHolder(running),
		processName: applystatus.Daemon,
	}
	b.applyStatus = applystatus.New(b.processName, recorder, b.log)
	return b, recorder
}

// settingsWatchStub answers Query with one stored settings document and Watch
// with a stream carrying the given documents. It is the seam the daemon really
// meets a live update through: the control plane stores documents, and
// controlplane.Client decodes them before the apply path sees anything.
type settingsWatchStub struct {
	stored    string
	version   int64
	documents []string
}

func (*settingsWatchStub) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	return &pb.CatalogResponse{}, nil
}

func (s *settingsWatchStub) Query(context.Context, *pb.QueryRequest, ...grpc.CallOption) (*pb.QueryResponse, error) {
	return &pb.QueryResponse{Resource: &pb.Resource{Kind: controlplane.WorkflowExecutionSettingsKind, Version: s.version, ValueJson: []byte(s.stored)}}, nil
}

func (*settingsWatchStub) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("unexpected History")
}

func (*settingsWatchStub) Command(context.Context, *pb.CommandRequest, ...grpc.CallOption) (*pb.CommandResponse, error) {
	panic("unexpected Command")
}

func (s *settingsWatchStub) Watch(context.Context, *pb.WatchRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	responses := make([]*pb.WatchResponse, 0, len(s.documents))
	for i, document := range s.documents {
		responses = append(responses, &pb.WatchResponse{Resource: &pb.Resource{
			Kind:      controlplane.WorkflowExecutionSettingsKind,
			Version:   s.version + int64(i) + 1,
			ValueJson: []byte(document),
		}})
	}
	return &watchStreamStub{responses: responses}, nil
}

// watchStreamStub is the client end of a Watch stream. Recv is the only method
// the settings watch calls; the embedded ClientStream is nil, so any other call
// panics rather than pretending to work.
type watchStreamStub struct {
	grpc.ClientStream
	responses []*pb.WatchResponse
	next      int
}

func (s *watchStreamStub) Recv() (*pb.WatchResponse, error) {
	if s.next >= len(s.responses) {
		return nil, io.EOF
	}
	response := s.responses[s.next]
	s.next++
	return response, nil
}

// TestLiveExecutionSettingsUpdateIsCheckedBeforeItReplacesTheRunningLimits is
// archie-core-nwa0. docs/prds/runtime-control-plane.md ("API") requires a live
// change to replace a running component only after the new one has been
// started and checked; if the check fails the old one keeps running. A live
// workflow-execution-settings update replaces the configuration snapshot every
// new task is built from, so a candidate whose snapshot this process cannot run
// must leave the running limits and the running snapshot in place, and the
// failure must reach the apply-status surface carrying the version that is
// still live.
func TestLiveExecutionSettingsUpdateIsCheckedBeforeItReplacesTheRunningLimits(t *testing.T) {
	tests := []struct {
		name        string
		candidate   workflow.ExecutionSettings
		wantRunning workflow.ExecutionSettings
		wantVersion int64
	}{
		{
			name:        "an update whose snapshot this process can run replaces the running limits",
			candidate:   workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4},
			wantRunning: workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4},
			wantVersion: 2,
		},
		{
			// Zero is the documented "this individual limit is off", so an
			// all-zero update is a legal live change, not a rejection, and the
			// check must not invent a bound the store does not enforce.
			name:        "an update that disables every limit is accepted",
			candidate:   workflow.ExecutionSettings{},
			wantRunning: workflow.ExecutionSettings{},
			wantVersion: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, status := newLiveApplyBoot(t, fileConfig())
			if err := b.applyWorkflowExecutionSettings(t.Context(), bootedSettings, 1); err != nil {
				t.Fatalf("applying the running settings: %v", err)
			}
			if err := b.applyWorkflowExecutionSettings(t.Context(), tt.candidate, 2); err != nil {
				t.Fatalf("live update: %v", err)
			}

			got := b.executionSettings.Load()
			if got == nil {
				t.Fatal("no settings are recorded as running")
			}
			if *got != tt.wantRunning {
				t.Errorf("running settings = %+v, want %+v", *got, tt.wantRunning)
			}

			// The snapshot a new task is built from has to move with the
			// settings: it is the half of the component the daemon runs.
			wantBudgets := budgetsFor(tt.wantRunning)
			if budgets := b.cfgHolder.Get().Budgets; budgets != wantBudgets {
				t.Errorf("running budgets = %+v, want %+v", budgets, wantBudgets)
			}

			last, ok := status.last(controlplane.WorkflowExecutionSettingsKind)
			if !ok {
				t.Fatal("the live update reported no apply status")
			}
			if last.AppliedVersion != tt.wantVersion {
				t.Errorf("apply status version = %d, want %d", last.AppliedVersion, tt.wantVersion)
			}
			if last.Error != "" {
				t.Errorf("apply status error = %q, want none for an applied update", last.Error)
			}
		})
	}
}

// TestLiveExecutionSettingsUpdateIsRefusedWhenTheSnapshotIsNotRunnable covers
// the refusal half of the same check. The candidate is the running snapshot
// with new limits written into it, and configuration.Validate does not read
// limits, so the only input that can fail the check is a snapshot this process
// would not run -- which is what this test's daemon is running. A live update
// must not publish a snapshot this process cannot run, must leave the component
// it would have replaced in place, and must report the refusal against the
// version still live.
func TestLiveExecutionSettingsUpdateIsRefusedWhenTheSnapshotIsNotRunnable(t *testing.T) {
	unrunnable := fileConfig()
	unrunnable.Containers.Image = ""
	applyExecutionBudgets(&unrunnable, bootedSettings)
	b, status := newLiveApplyBoot(t, unrunnable)
	// The daemon is already running these limits. Seeded directly rather than
	// applied: the ordinary apply refuses this snapshot too, which is the point
	// of the check.
	b.executionSettings.Store(&bootedSettings)
	b.applyStatus.Report(t.Context(), controlplane.WorkflowExecutionSettingsKind, 1, nil)

	budgets := budgetsFor(bootedSettings)
	update := workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4}
	if err := b.applyWorkflowExecutionSettings(t.Context(), update, 2); err == nil {
		t.Fatal("an update whose snapshot this process cannot run was applied")
	}

	if got := b.executionSettings.Load(); got == nil || *got != bootedSettings {
		t.Errorf("running settings = %v, want the refused update to leave %+v running", got, bootedSettings)
	}
	if got := b.cfgHolder.Get().Budgets; got != budgets {
		t.Errorf("running budgets = %+v, want the refused update to leave %+v running", got, budgets)
	}

	last, ok := status.last(controlplane.WorkflowExecutionSettingsKind)
	if !ok {
		t.Fatal("the refused update reported no apply status")
	}
	if last.Error == "" {
		t.Error("the refusal was not reported through apply status")
	}
	if last.AppliedVersion != 1 {
		t.Errorf("apply status version = %d, want the version still live (1)", last.AppliedVersion)
	}
}

// TestLiveExecutionSettingsRefusalIsReportedThroughTheClient is the other half
// of the same item, over the seam the daemon actually meets a refusal through.
// controlplane.Client decodes every document it is handed, so an update this
// process cannot run arrives as a watch error, never as invalid settings: the
// watch branch must report that refusal against the version still live instead
// of only logging it, or the settings page shows a live resource as healthy
// while nothing has been applied. (That the stream is not re-established
// afterwards is archie-core-yrmr, not this apply path.)
func TestLiveExecutionSettingsRefusalIsReportedThroughTheClient(t *testing.T) {
	tests := []struct {
		name              string
		document          string
		wantRunning       workflow.ExecutionSettings
		wantVersion       int64
		wantErrorContains string
	}{
		{
			name:        "an update the process can run is applied",
			document:    `{"max_model_tool_steps": 25, "max_runtime_seconds": 3600, "max_consecutive_gate_failures": 4}`,
			wantRunning: workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4},
			wantVersion: 2,
		},
		{
			name:              "an update the client refuses is reported against the version still live",
			document:          `{"max_model_tool_steps": -1, "max_runtime_seconds": 3600, "max_consecutive_gate_failures": 4}`,
			wantRunning:       bootedSettings,
			wantVersion:       1,
			wantErrorContains: "max model/tool steps must not be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, status := newLiveApplyBoot(t, fileConfig())
			b.controlPlane = controlplane.NewRPCClient(&settingsWatchStub{stored: bootedDocument, version: 1, documents: []string{tt.document}})

			if err := b.startWorkflowExecutionSettings(t.Context()); err != nil {
				t.Fatalf("startWorkflowExecutionSettings: %v", err)
			}
			// One record for boot, one for the watched update.
			records := status.awaitCount(t, controlplane.WorkflowExecutionSettingsKind, 2)

			got := b.executionSettings.Load()
			if got == nil {
				t.Fatal("no settings are recorded as running")
			}
			if *got != tt.wantRunning {
				t.Errorf("running settings = %+v, want %+v", *got, tt.wantRunning)
			}
			if budgets := b.cfgHolder.Get().Budgets; budgets != budgetsFor(tt.wantRunning) {
				t.Errorf("running budgets = %+v, want %+v", budgets, budgetsFor(tt.wantRunning))
			}

			last := records[len(records)-1]
			if last.AppliedVersion != tt.wantVersion {
				t.Errorf("apply status version = %d, want %d", last.AppliedVersion, tt.wantVersion)
			}
			if tt.wantErrorContains == "" && last.Error != "" {
				t.Errorf("apply status error = %q, want none for an applied update", last.Error)
			}
			if tt.wantErrorContains != "" && !strings.Contains(last.Error, tt.wantErrorContains) {
				t.Errorf("apply status error = %q, want it to carry %q", last.Error, tt.wantErrorContains)
			}
		})
	}
}

// budgetsFor is the configuration snapshot a set of limits produces. Named so
// the tests and applyExecutionBudgets cannot drift on the mapping.
func budgetsFor(settings workflow.ExecutionSettings) config.Budgets {
	return config.Budgets{
		MaxSteps:        settings.MaxModelToolSteps,
		WallClock:       config.Duration(settings.MaxRuntime),
		GateMaxFailures: settings.MaxConsecutiveGateFailures,
	}
}
