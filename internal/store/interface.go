// Package store defines the task and event persistence for archied's data.
// TaskStore is the full surface the daemon needs and *Store is its SQLite
// implementation.
//
// REVISED (archie-core-8cda.5.6): the producer-owned contract surfaces this
// package used to define live in internal/contracts/store/v1 (package
// storev1) -- the approved contract location -- so the archie-ui process can
// reference the contract without linking this SQLite implementation. The
// definitions below are type aliases for compatibility with the daemon-side
// callers; new UI-process-facing code imports storev1 directly.
package store

import (
	storev1 "github.com/samcharles93/archie-core/internal/contracts/store/v1"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// Aliases: the daemon/webui/intake store surfaces moved to storev1. The
// message strings of the sentinel errors are the wire contract (see
// storev1's var block); the aliases change nothing observable.
type (
	TaskStore           = storev1.TaskStore
	TaskLifecycle       = storev1.TaskLifecycle
	TaskArchiver        = storev1.TaskArchiver
	TaskRetryer         = storev1.TaskRetryer
	TaskQueries         = storev1.TaskQueries
	TaskEvents          = storev1.TaskEvents
	CaptureStore        = storev1.CaptureStore
	ConfigSnapshotStore = storev1.ConfigSnapshotStore
	MappingStore        = storev1.MappingStore
	BindingStore        = storev1.BindingStore
	BindingDispatcher   = storev1.BindingDispatcher
	BindingTaskCreator  = storev1.BindingTaskCreator
	CapturedEvent       = storev1.CapturedEvent
	ConfigSnapshot      = storev1.ConfigSnapshot
	WorkflowStat        = storev1.WorkflowStat
	StageStat           = storev1.StageStat
	DayTokens           = storev1.DayTokens
)

var (
	ErrStaleTransition   = storev1.ErrStaleTransition
	ErrBindingNotFound   = storev1.ErrBindingNotFound
	ErrBindingOverlap    = storev1.ErrBindingOverlap
	ErrBindingTransition = storev1.ErrBindingTransition
	ErrAlreadyDispatched = storev1.ErrAlreadyDispatched
	ErrMappingNotFound   = storev1.ErrMappingNotFound
)

// Compile-time check: *Store satisfies TaskStore.
var _ TaskStore = (*Store)(nil)

// Compile-time check: *Store satisfies workflow.Store.
var _ workflow.Store = (*Store)(nil)

// Compile-time check: *Store satisfies CaptureStore.
var _ CaptureStore = (*Store)(nil)

// Compile-time check: *Store satisfies MappingStore.
var _ MappingStore = (*Store)(nil)

// Compile-time check: *Store satisfies BindingStore.
var _ BindingStore = (*Store)(nil)

// Compile-time check: *Store satisfies BindingDispatcher.
var _ BindingDispatcher = (*Store)(nil)

// Compile-time check: *Store satisfies BindingTaskCreator.
var _ BindingTaskCreator = (*Store)(nil)
