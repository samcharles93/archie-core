package workflow

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

func commandRegistry() StepRegistry {
	registry := BuiltinStepRegistry()
	registry[CommandRunStepName] = newCommandRunStage
	return registry
}

// compileCommandStep parses and compiles a one-step definition through the real
// registry, so every test below drives the same parse -> compile -> run path a
// stored definition does rather than calling the factory directly.
func compileCommandStep(t *testing.T, settings string) Stage {
	t.Helper()
	src := "id: w\nsteps:\n  - type: " + CommandRunStepName + "\n"
	if settings != "" {
		src += "    settings:\n" + settings
	}
	wf, err := ParseAndCompile(src, commandRegistry())
	if err != nil {
		t.Fatalf("ParseAndCompile: %v", err)
	}
	if len(wf.Stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(wf.Stages))
	}
	return wf.Stages[0]
}

// TestCommandRunCarriesEveryGateCommandField holds the settings shape to the
// gate's command type in both directions. The settings are decoded from YAML
// and the gate's type carries wire (JSON) tags only, so the two are
// hand-mirrored: a field present in one and missing from the other would let a
// definition say one thing while the run did another, which is how
// expect_failure was first dropped here. It is the same reflection guard the
// control plane uses for its projected settings types.
func TestCommandRunCarriesEveryGateCommandField(t *testing.T) {
	settings := reflect.TypeFor[commandRunCommand]()
	gate := reflect.TypeFor[agentexec.Command]()

	for field := range gate.Fields() {
		name := field.Name
		if _, ok := settings.FieldByName(name); !ok {
			t.Errorf("agentexec.Command.%s has no commandRunCommand counterpart, so no definition could set it", name)
		}
	}
	for field := range settings.Fields() {
		name := field.Name
		if _, ok := gate.FieldByName(name); !ok {
			t.Errorf("commandRunCommand.%s has no agentexec.Command counterpart, so the value would be dropped", name)
		}
	}
}

// TestCommandRunRefusesMalformedSettings keeps the refusals at the producer: a
// definition that cannot mean anything is rejected when it is saved, not when a
// run reaches the step with no workspace or no command to run.
func TestCommandRunRefusesMalformedSettings(t *testing.T) {
	for _, test := range []struct{ name, settings, want string }{
		{"no settings at all", "", "settings.run is required"},
		{"empty run list", "      run: []\n", "settings.run needs at least one command"},
		{"a command with no argv", "      run:\n        - name: check\n", "settings.run[0].argv is required"},
		{"an unknown level", "      level: shout\n      run:\n        - argv: [\"true\"]\n", `settings.level "shout"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "id: w\nsteps:\n  - type: " + CommandRunStepName + "\n"
			if test.settings != "" {
				src += "    settings:\n" + test.settings
			}
			_, err := ParseAndCompile(src, commandRegistry())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseAndCompile() error = %v, want one containing %q", err, test.want)
			}
		})
	}
}

// TestCommandRunExecutesArgvInTheWorkspace is the step's whole purpose: the
// operator's argv runs in the task worktree, so a command can inspect or change
// the tree its position in the definition put it next to.
func TestCommandRunExecutesArgvInTheWorkspace(t *testing.T) {
	stage := compileCommandStep(t, "      run:\n        - name: write\n          argv: [\"sh\", \"-c\", \"printf hi > made.txt\"]\n")
	dir := t.TempDir()

	if err := stage.Run(context.Background(), &TaskContext{Dir: dir}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "made.txt"))
	if err != nil {
		t.Fatalf("the command did not run in the workspace: %v", err)
	}
	if string(got) != "hi" {
		t.Fatalf("made.txt = %q, want %q", got, "hi")
	}
}

// TestCommandRunParksOnNonZeroExit is the decision's rule: a failing command
// parks the run rather than failing the attempt, so an operator can fix what the
// command found and retry.
func TestCommandRunParksOnNonZeroExit(t *testing.T) {
	stage := compileCommandStep(t, "      run:\n        - name: verify\n          argv: [\"sh\", \"-c\", \"echo broken >&2; exit 3\"]\n")
	tc := &TaskContext{Dir: t.TempDir()}

	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("Run() = %v, want nil: a failing command parks, it does not fail the attempt", err)
	}
	if tc.Outcome.Status != StatusParked {
		t.Fatalf("Outcome.Status = %q, want %q", tc.Outcome.Status, StatusParked)
	}
	if !strings.Contains(tc.Outcome.Detail, "verify") || !strings.Contains(tc.Outcome.Detail, "broken") {
		t.Fatalf("park Detail = %q, want it to name the command and carry its output", tc.Outcome.Detail)
	}
}

// TestCommandRunWarnLevelDoesNotPark is the advisory carve-out: level warn
// reports the failure and the workflow continues.
func TestCommandRunWarnLevelDoesNotPark(t *testing.T) {
	stage := compileCommandStep(t, "      level: warn\n      run:\n        - name: advisory-check\n          argv: [\"sh\", \"-c\", \"exit 1\"]\n")
	tc := &TaskContext{Dir: t.TempDir()}

	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if tc.Outcome.Status != "" {
		t.Fatalf("Outcome.Status = %q, want empty: level warn must not stop the run", tc.Outcome.Status)
	}
}

// TestCommandRunExpectFailureInvertsTheResult covers both directions of the
// gate's inversion, because only one of them is visible from the success path:
// an expected failure that does not happen is itself the failure.
func TestCommandRunExpectFailureInvertsTheResult(t *testing.T) {
	t.Run("an expected failure does not park", func(t *testing.T) {
		stage := compileCommandStep(t, "      run:\n        - name: must-fail\n          argv: [\"sh\", \"-c\", \"exit 1\"]\n          expect_failure: true\n")
		tc := &TaskContext{Dir: t.TempDir()}
		if err := stage.Run(context.Background(), tc); err != nil {
			t.Fatalf("Run() = %v", err)
		}
		if tc.Outcome.Status != "" {
			t.Fatalf("Outcome.Status = %q, want empty", tc.Outcome.Status)
		}
	})

	t.Run("a command that passes when it should fail parks", func(t *testing.T) {
		stage := compileCommandStep(t, "      run:\n        - name: must-fail\n          argv: [\"true\"]\n          expect_failure: true\n")
		tc := &TaskContext{Dir: t.TempDir()}
		if err := stage.Run(context.Background(), tc); err != nil {
			t.Fatalf("Run() = %v", err)
		}
		if tc.Outcome.Status != StatusParked {
			t.Fatalf("Outcome.Status = %q, want %q", tc.Outcome.Status, StatusParked)
		}
		if !strings.Contains(tc.Outcome.Detail, "expected this command to fail") {
			t.Fatalf("park Detail = %q, want it to say the command should have failed", tc.Outcome.Detail)
		}
	})
}

// TestCommandRunStopsAtTheFirstFailure pins that a later command does not run
// after an earlier one failed: running the rest would report a state the
// operator did not ask for, and could mutate the tree after the run was already
// decided to park.
func TestCommandRunStopsAtTheFirstFailure(t *testing.T) {
	stage := compileCommandStep(t, strings.Join([]string{
		"      run:",
		"        - name: first",
		"          argv: [\"sh\", \"-c\", \"exit 2\"]",
		"        - name: second",
		"          argv: [\"sh\", \"-c\", \"printf later > should-not-exist.txt\"]",
		"",
	}, "\n"))
	dir := t.TempDir()
	tc := &TaskContext{Dir: dir}

	if err := stage.Run(context.Background(), tc); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist.txt")); !os.IsNotExist(err) {
		t.Fatalf("the second command ran after the first failed (stat err = %v)", err)
	}
}

// TestCommandRunWithoutAWorkspaceFails names the composition mistake instead of
// running the command in whatever directory the process happens to be in.
func TestCommandRunWithoutAWorkspaceFails(t *testing.T) {
	stage := compileCommandStep(t, "      run:\n        - name: write\n          argv: [\"true\"]\n")
	err := stage.Run(context.Background(), &TaskContext{})
	if err == nil || !strings.Contains(err.Error(), "no workspace") {
		t.Fatalf("Run() error = %v, want one naming the missing workspace", err)
	}
}
