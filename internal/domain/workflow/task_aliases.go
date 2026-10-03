package workflow

import "github.com/samcharles93/archie-core/internal/domain/workflow/task"

type (
	// Task is the task-execution record the workflow domain operates on.
	Task = task.Task
	// Store is the narrow, consumer-owned subset of the State Store
	// contract that workflow stages call mid-run.
	Store = task.Store
	// StepStart and StepFinish are the step-execution writes the store
	// records mid-run.
	StepStart  = task.StepStart
	StepFinish = task.StepFinish
	// Definition is the operator-safe snapshot of an executable workflow.
	Definition = task.Definition
)

// Step-execution kinds, defined beside the step writes in internal/domain/
// workflow/task with the same aliasing reason.
const (
	StepKindStage = task.StepKindStage
	StepKindAgent = task.StepKindAgent
	StepKindCall  = task.StepKindCall
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
	StatusCompleted    = task.StatusCompleted
)

// Task.Source values.
const (
	SourceForge = task.SourceForge
	SourceChat  = task.SourceChat
)
