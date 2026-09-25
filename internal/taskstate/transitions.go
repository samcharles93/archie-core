package taskstate

import (
	"errors"
	"fmt"
	"slices"
)

// StepStatus is the lifecycle of a StepExecution -- one stage run, or one agent
// call a stage makes, inside a WorkflowExecution
// (docs/prds/execution-tree-state-machine.md). It is part of the on-disk
// format, like the execution statuses above.
//
// It is a distinct type from those statuses even though the two vocabularies
// share the word "running": an execution status says what the task is doing,
// a step status says what one stage is doing, and the two move through
// different tables. One shared string type would let either status be passed
// where the other belongs, which is the mistake the separate tables exist to
// catch.
type StepStatus string

const (
	StepPending     StepStatus = "pending"
	StepRunning     StepStatus = "running"
	StepSucceeded   StepStatus = "succeeded"
	StepFailed      StepStatus = "failed"
	StepCancelled   StepStatus = "cancelled"
	StepInterrupted StepStatus = "interrupted"
)

// executionTransitions is the WorkflowExecution table: the complete set of
// legal status moves, held as data so no writer carries its own conditional
// copy of the rules.
//
// Terminal statuses appear as empty rows rather than being absent, so the
// table is total over the vocabulary: a status that is missing here is
// unroutable, and the difference between "goes nowhere" and "nobody wrote a
// row" stays visible.
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

// CheckStepStart reports whether a StepExecution may enter StepRunning under
// the execution it belongs to.
//
// A step only runs while its execution is running, and never under a parent
// that has already finished: work a terminal parent had going was cancelled or
// interrupted when it finished, so a step starting afterwards would be recorded
// against a decision that has already been taken.
func CheckStepStart(executionStatus string, parentTerminal bool) error {
	if executionStatus != Running {
		return fmt.Errorf("step cannot start while its execution is %s", executionStatus)
	}
	if parentTerminal {
		return errors.New("step cannot start under a terminal parent")
	}
	return nil
}
