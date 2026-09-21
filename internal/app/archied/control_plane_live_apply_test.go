package archied

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
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

// newLiveApplyBoot builds a boot whose live kinds report apply status through
// a recorder, so a test can read back what the settings page would show.
func newLiveApplyBoot(t *testing.T) (*boot, *applyStatusRecorder) {
	t.Helper()
	recorder := &applyStatusRecorder{}
	b := &boot{
		cfg:         fileConfig(),
		log:         slog.New(slog.DiscardHandler),
		cfgHolder:   config.NewHolder(fileConfig()),
		processName: applystatus.Daemon,
	}
	b.applyStatus = applystatus.New(b.processName, recorder, b.log)
	return b, recorder
}

// TestLiveExecutionSettingsUpdateIsCheckedBeforeItReplacesTheRunningLimits is
// archie-core-nwa0. docs/prds/runtime-control-plane.md ("API") requires a live
// change to replace a running component only after the new one has been
// started and checked; if the check fails the old one keeps running. A live
// workflow-execution-settings update replaces the limits every new task is
// built from, so a candidate the component refuses must leave the running
// limits and the running configuration snapshot in place, and the rejection
// must reach the apply-status surface carrying the version that is still live.
func TestLiveExecutionSettingsUpdateIsCheckedBeforeItReplacesTheRunningLimits(t *testing.T) {
	// The limits the daemon is already running when the update arrives.
	running := workflow.ExecutionSettings{MaxModelToolSteps: 10, MaxRuntime: 5 * time.Minute, MaxConsecutiveGateFailures: 3}

	tests := []struct {
		name        string
		candidate   workflow.ExecutionSettings
		wantRunning workflow.ExecutionSettings
		wantError   bool
		wantVersion int64
	}{
		{
			name:        "an update the component accepts replaces the running limits",
			candidate:   workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4},
			wantRunning: workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4},
			wantVersion: 2,
		},
		{
			// Zero is the documented "this individual limit is off", so an
			// all-zero update is a legal live change, not a rejection.
			name:        "an update that disables every limit is accepted",
			candidate:   workflow.ExecutionSettings{},
			wantRunning: workflow.ExecutionSettings{},
			wantVersion: 2,
		},
		{
			name:        "an update with a negative step limit is refused and the running limits stay",
			candidate:   workflow.ExecutionSettings{MaxModelToolSteps: -1, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4},
			wantRunning: running,
			wantError:   true,
			wantVersion: 1,
		},
		{
			name:        "an update with a negative runtime is refused and the running limits stay",
			candidate:   workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: -time.Second, MaxConsecutiveGateFailures: 4},
			wantRunning: running,
			wantError:   true,
			wantVersion: 1,
		},
		{
			name:        "an update with a negative gate-failure limit is refused and the running limits stay",
			candidate:   workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: -1},
			wantRunning: running,
			wantError:   true,
			wantVersion: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, status := newLiveApplyBoot(t)
			if err := b.applyWorkflowExecutionSettings(t.Context(), running, 1); err != nil {
				t.Fatalf("applying the running settings: %v", err)
			}
			if err := b.applyWorkflowExecutionSettings(t.Context(), tt.candidate, 2); (err != nil) != tt.wantError {
				t.Fatalf("live update error = %v, want an error: %v", err, tt.wantError)
			}

			got := b.executionSettings.Load()
			if got == nil {
				t.Fatal("no settings are recorded as running")
			}
			if *got != tt.wantRunning {
				t.Errorf("running settings = %+v, want %+v", *got, tt.wantRunning)
			}

			// The snapshot a new task is built from must move with the
			// settings, and must not move for a refused update.
			wantBudgets := config.Budgets{
				MaxSteps:        tt.wantRunning.MaxModelToolSteps,
				WallClock:       config.Duration(tt.wantRunning.MaxRuntime),
				GateMaxFailures: tt.wantRunning.MaxConsecutiveGateFailures,
			}
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
			if (last.Error != "") != tt.wantError {
				t.Errorf("apply status error = %q, want an error: %v", last.Error, tt.wantError)
			}
		})
	}
}
