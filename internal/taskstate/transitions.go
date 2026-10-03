package taskstate

import (
	"errors"
	"fmt"
	"slices"
)

// StepStatus is a step execution's status. Stored.
type StepStatus string

const (
	StepPending     StepStatus = "pending"
	StepRunning     StepStatus = "running"
	StepSucceeded   StepStatus = "succeeded"
	StepFailed      StepStatus = "failed"
	StepCancelled   StepStatus = "cancelled"
	StepInterrupted StepStatus = "interrupted"
)

// executionTransitions lists every legal execution status move. Terminal
// statuses have empty rows.
var executionTransitions = map[string][]string{
	Queued:       {Running, Declined},
	Running:      {Queued, WaitingHuman, PROpen, Completed, Parked, Declined},
	WaitingHuman: {Queued, Declined},
	Parked:       {Queued, Dead, Declined},
	PROpen:       {Merged, Rejected, Queued, Declined},

	Merged:    nil,
	Rejected:  nil,
	Dead:      nil,
	Declined:  nil,
	Completed: nil,
}

// stepTransitions is the StepExecution table, the same shape over the step
// vocabulary.
var stepTransitions = map[StepStatus][]StepStatus{
	StepPending: {StepRunning, StepCancelled},
	StepRunning: {StepSucceeded, StepFailed, StepCancelled, StepInterrupted},

	StepSucceeded:   nil,
	StepFailed:      nil,
	StepCancelled:   nil,
	StepInterrupted: nil,
}

// CanTransition reports whether a WorkflowExecution may move from one status to
// another. The pair must appear in executionTransitions: an unknown status on
// either side is illegal, and a terminal status has no way out.
func CanTransition(from, to string) bool {
	return slices.Contains(executionTransitions[from], to)
}

// CanStepTransition reports whether a StepExecution may move from one status to
// another, under the same rules.
func CanStepTransition(from, to StepStatus) bool {
	return slices.Contains(stepTransitions[from], to)
}

// StepTerminal reports whether a step status is an end state. A finished step
// only moves again as a fresh step in a new attempt.
func StepTerminal(status StepStatus) bool {
	switch status {
	case StepSucceeded, StepFailed, StepCancelled, StepInterrupted:
		return true
	default:
		return false
	}
}

// CheckStepStart allows a step to start only while its execution is running
// and its parent has not finished.
func CheckStepStart(executionStatus string, parentTerminal bool) error {
	if executionStatus != Running {
		return fmt.Errorf("step cannot start while its execution is %s", executionStatus)
	}
	if parentTerminal {
		return errors.New("step cannot start under a terminal parent")
	}
	return nil
}
