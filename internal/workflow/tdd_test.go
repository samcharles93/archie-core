package workflow

import (
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestTDDGateUsesLastNonEmptyCommand(t *testing.T) {
	repo := config.Repo{Gate: [][]string{
		{"go", "vet", "./..."},
		{"go", "test", "./..."},
		{},
	}}

	gate := tddReproGate(repo, config.Budgets{GateMaxFailures: 3})
	if len(gate.Commands) != 2 {
		t.Fatalf("gate has %d commands, want 2", len(gate.Commands))
	}
	if gate.Commands[0].ExpectFailure {
		t.Error("first gate command unexpectedly requires failure")
	}
	if !gate.Commands[1].ExpectFailure {
		t.Error("last non-empty gate command does not require failure")
	}
	if got, want := testCommandArgv(repo), []string{"go", "test", "./..."}; !reflect.DeepEqual(got, want) {
		t.Errorf("testCommandArgv() = %v, want %v", got, want)
	}
	if got, want := testCommand(repo), "go test ./..."; got != want {
		t.Errorf("testCommand() = %q, want %q", got, want)
	}
}

func TestTDDGateWithOnlyEmptyCommands(t *testing.T) {
	repo := config.Repo{Gate: [][]string{{}, nil}}

	if got := tddReproGate(repo, config.Budgets{}).Commands; len(got) != 0 {
		t.Errorf("gate commands = %v, want none", got)
	}
	if got := testCommandArgv(repo); got != nil {
		t.Errorf("testCommandArgv() = %v, want nil", got)
	}
	if got := testCommand(repo); got != "(no gate configured)" {
		t.Errorf("testCommand() = %q, want no-gate message", got)
	}
}
