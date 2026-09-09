// Task-vocabulary aliases. The Task record, its statuses, the mid-run
// Store contract, and Definition moved to internal/domain/workflow/task
// (archie-core-8cda.5.6) so the UI process can hold the vocabulary without
// linking this package's pipeline engine (which imports agentexec, skill
// and tools). package workflow keeps these aliases, so daemon-side callers
// are unaffected.
package workflow

import "github.com/samcharles93/archie-core/internal/domain/workflow/task"

type (
	// Task is the task-execution record the workflow domain operates on.
	Task = task.Task
	// Store is the narrow, consumer-owned subset of the State Store
	// contract that workflow stages call mid-run.
	Store = task.Store
	// Definition is the operator-safe snapshot of an executable workflow.
	Definition = task.Definition
)

// Task lifecycle statuses. Defined in internal/taskstate; these names are
// the workflow domain's public spelling.
const (
	StatusQueued       = task.StatusQueued
	StatusRunning      = task.StatusRunning
	StatusWaitingHuman = task.StatusWaitingHuman
	StatusPROpen       = task.StatusPROpen
	StatusMerged       = task.StatusMerged
	StatusParked       = task.StatusParked
	StatusDead         = task.StatusDead
	StatusRejected     = task.StatusRejected
	StatusClosedWontDo = task.StatusClosedWontDo
)

// Task.Source values.
const (
	SourceForge = task.SourceForge
	SourceChat  = task.SourceChat
)
