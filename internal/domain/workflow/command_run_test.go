package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// settingsNode renders a command.run settings value the way a definition
// carries it: a yaml.Node the factory decodes.
func settingsNode(t *testing.T, s commandRunSettings) yaml.Node {
	t.Helper()
	raw, err := yaml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		t.Fatalf("settings node: %v", err)
	}
	return n
}

// TestCommandRunObeysStepControls pins that a failing command.run step is a
// step failure like any other: retry re-runs it, on_failure: continue keeps
// the run going, and only level: warn is advisory. Reporting the failure as
// a parked outcome instead left all three controls unapplied.
func TestCommandRunObeysStepControls(t *testing.T) {
	tests := []struct {
		name          string
		level         string
		onFailure     string
		attempts      int
		wantRuns      int
		wantErr       bool
		wantContinues bool
	}{
		{
			name:     "retry re-runs a failed command",
			attempts: 3,
			wantRuns: 3,
			wantErr:  true,
		},
		{
			name:          "on_failure continue keeps the run going",
			onFailure:     onFailureContinue,
			wantRuns:      1,
			wantErr:       true,
			wantContinues: true,
		},
		{
			name:     "level warn is advisory",
			level:    commandRunLevelWarn,
			wantRuns: 1,
		},
	}
	registry := StepRegistry{CommandRunStepName: newCommandRunStage}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "runs")
			settings := commandRunSettings{
				Level: tt.level,
				Run: []commandRunCommand{{
					Name: "probe",
					Argv: []string{"sh", "-c", fmt.Sprintf("echo run >> %q; exit 1", marker)},
				}},
			}
			step := StepRecord{Type: CommandRunStepName, Settings: settingsNode(t, settings), OnFailure: tt.onFailure}
			if tt.attempts > 0 {
				step.Retry = &RetryPolicy{Attempts: tt.attempts}
			}
			stage, err := compileStep(step, registry)
			if err != nil {
				t.Fatalf("compile step: %v", err)
			}
			if stage.ContinueOnFailure != tt.wantContinues {
				t.Fatalf("ContinueOnFailure = %v, want %v", stage.ContinueOnFailure, tt.wantContinues)
			}
			tc := &TaskContext{Task: &task.Task{ID: 1}, Dir: t.TempDir(), Log: slog.Default()}
			runErr := stage.Run(context.Background(), tc)
			if (runErr != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", runErr, tt.wantErr)
			}
			if runErr != nil && strings.TrimSpace(runErr.Error()) == "" {
				t.Fatal("failure carried no detail for the park reason")
			}
			raw, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("read marker: %v", err)
			}
			if got := len(strings.Fields(string(raw))); got != tt.wantRuns {
				t.Fatalf("command ran %d times, want %d", got, tt.wantRuns)
			}
		})
	}
}
