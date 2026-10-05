package taskstate

import "testing"

// The step guard is the store's own enforcement of which step moves are legal.
// A skipped step is a running step's outcome and a terminal state: one that
// never started cannot skip, and one that skipped never moves again.
func TestStepTransitions(t *testing.T) {
	tests := []struct {
		name     string
		from, to StepStatus
		want     bool
	}{
		{"a running step is skipped", StepRunning, StepSkipped, true},
		{"a running step still succeeds", StepRunning, StepSucceeded, true},
		{"a pending step is not skipped", StepPending, StepSkipped, false},
		{"a skipped step does not move", StepSkipped, StepRunning, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanStepTransition(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanStepTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}
