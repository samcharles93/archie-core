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
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// Aliases: the daemon/webui/intake store surfaces moved to storecontract. The
// message strings of the sentinel errors are the wire contract (see
// storev1's var block); the aliases change nothing observable.
type (
	TaskStore           = storecontract.TaskStore
	TaskLifecycle       = storecontract.TaskLifecycle
	TaskArchiver        = storecontract.TaskArchiver
	TaskRetryer         = storecontract.TaskRetryer
	TaskQueries         = storecontract.TaskQueries
	TaskEvents          = storecontract.TaskEvents
	CaptureStore        = storecontract.CaptureStore
	ConfigSnapshotStore = storecontract.ConfigSnapshotStore
	MappingStore        = storecontract.MappingStore
	BindingStore        = storecontract.BindingStore
	BindingDispatcher   = storecontract.BindingDispatcher
	BindingTaskCreator  = storecontract.BindingTaskCreator
	CapturedEvent       = storecontract.CapturedEvent
	ConfigSnapshot      = storecontract.ConfigSnapshot
	WorkflowStat        = storecontract.WorkflowStat
	StageStat           = storecontract.StageStat
	DayTokens           = storecontract.DayTokens
)

var (
	ErrStaleTransition   = storecontract.ErrStaleTransition
	ErrBindingNotFound   = storecontract.ErrBindingNotFound
	ErrBindingOverlap    = storecontract.ErrBindingOverlap
	ErrBindingTransition = storecontract.ErrBindingTransition
	ErrAlreadyDispatched = storecontract.ErrAlreadyDispatched
	ErrMappingNotFound   = storecontract.ErrMappingNotFound
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
