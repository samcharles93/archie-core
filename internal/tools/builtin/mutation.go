package builtin

import (
	"sync"
)

// MutationQueue serializes writes per file path. File mutations share a read
// lock; shell commands take the write lock.
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

// Acquire locks path and returns the release function. It blocks while a
// shell command runs.
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
