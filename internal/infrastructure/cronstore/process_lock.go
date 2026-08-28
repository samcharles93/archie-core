package cronstore

import "sync"

// processLocks serialises Stores within one process. It exists because
// Linux flock is per file description, not per inode: two Store
// instances opening the same jobs.json.lock each get their own fd and
// their own independent flock state, so they would not block each
// other inside a single process. The cross-process lock handles the
// CLI-vs-daemon case; this map handles the in-process case.
//
// Keyed by absolute lock-file path so two unrelated cronstores in the
// same process do not contend. Entries are created lazily and live for
// the life of the process; the map is append-only and small in
// practice (one entry per distinct config dir).
var (
	processLocksMu sync.Mutex
	processLocks   = map[string]*sync.Mutex{}
)

// processLockFor returns the per-lock-path mutex, creating it on first
// use. Callers must hold it for the duration of an operation that has
// already acquired the cross-process flock.
func processLockFor(path string) *sync.Mutex {
	processLocksMu.Lock()
	defer processLocksMu.Unlock()
	if m, ok := processLocks[path]; ok {
		return m
	}
	m := &sync.Mutex{}
	processLocks[path] = m
	return m
}
