// Lifted from tau internal/agent/tools/mutation.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
package builtin

import (
	"sync"
)

// MutationQueue serializes write operations to the same file path,
// preventing concurrent edits from clobbering each other during
// parallel tool execution.
//
// A sync.RWMutex coordinates between shell commands and file-mutation
// tools. File mutations (write, edit) take a read lock so they
// can run concurrently with each other. Shell commands take the write
// lock, blocking all file mutations for the duration of the command.
type MutationQueue struct {
	mu    sync.Mutex
	locks map[string]*mutexEntry

	globalMu sync.RWMutex
}

// mutexEntry tracks a per-file mutex and its active holder count.
type mutexEntry struct {
	mu      sync.Mutex
	holders int
}

// NewMutationQueue creates a new per-file mutation queue.
func NewMutationQueue() *MutationQueue {
	return &MutationQueue{
		locks: make(map[string]*mutexEntry),
	}
}

// GlobalLock blocks until all in-flight per-file mutations complete, then
// prevents new per-file Acquire calls from proceeding until GlobalUnlock
// is called. Used by the shell tool to ensure no race between shell
// commands and file-mutation tools.
func (q *MutationQueue) GlobalLock() {
	q.globalMu.Lock()
}

// GlobalUnlock releases the global lock, allowing per-file mutations to
// proceed again.
func (q *MutationQueue) GlobalUnlock() {
	q.globalMu.Unlock()
}

// Acquire returns a lock for the given file path. The caller must call
// the returned release function when done with the mutation.
//
// Acquire blocks while the global write lock is held (i.e. while a shell
// command is running).
//
// Usage:
//
//	release := q.Acquire("/path/to/file.go")
//	defer release()
//	// ... perform read-modify-write ...
func (q *MutationQueue) Acquire(path string) (release func()) {
	// Take a read lock so shell commands (which take the write lock)
	// block until we're done, and we block while a shell command runs.
	q.globalMu.RLock()

	q.mu.Lock()
	entry, ok := q.locks[path]
	if !ok {
		entry = &mutexEntry{}
		q.locks[path] = entry
	}
	entry.holders++
	q.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()

		q.mu.Lock()
		entry.holders--
		if entry.holders == 0 {
			delete(q.locks, path)
		}
		q.mu.Unlock()

		q.globalMu.RUnlock()
	}
}
