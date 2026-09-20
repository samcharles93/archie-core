package task

// ChangeStats is the change one attempt produced, measured at the moment it
// was committed or pushed.
//
// It is a measurement, not a view: the worktree it was read from is deleted on
// merge, close and no-PR terminal states, and a retry resets the branch onto
// its base, so this is the only record of what an attempt actually changed.
// Nothing re-derives it after the fact.
//
// The value types live in this subpackage because package workflow must not
// import internal/worktree. Both the workflow engine and persistence boundary
// can use the measurement without depending on git infrastructure.
type ChangeStats struct {
	BaseSHA string       `json:"base_sha"`
	HeadSHA string       `json:"head_sha"`
	Files   []FileChange `json:"files"`
	Totals  ChangeTotals `json:"totals"`
}

// FileChange is one file's contribution to a change.
type FileChange struct {
	Path string `json:"path"`
	// OldPath is the pre-change path, populated only for a rename.
	OldPath string `json:"old_path"`
	// Status is one of the Change* values below.
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Binary reports that the file produced no textual hunks. It is NOT a
	// claim that the file is binary on disk: an empty or otherwise chunkless
	// edit reports this too, so it must never be rendered as a file type.
	Binary bool `json:"binary"`
}

// ChangeTotals summarises a change. Totals always cover the FULL set of
// changed files, even when FileChange entries were dropped by the
// MaxCapturedFiles cap, so a truncated capture still reports honest totals.
type ChangeTotals struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

// Change status values as they are persisted inside a changes_captured
// event's data. Like the task lifecycle statuses, these strings are part of
// the on-disk format: renaming one is a migration, not a rename.
const (
	ChangeAdded       = "added"
	ChangeModified    = "modified"
	ChangeDeleted     = "deleted"
	ChangeRenamed     = "renamed"
	ChangeTypeChanged = "typechange"
)

// MaxCapturedFiles caps how many file entries one capture persists. Unlike
// Event.Detail, an event's data is not length-limited by the store, so this cap
// is what keeps one large refactor from writing an unbounded payload. A capture
// that dropped entries keeps the totals of the full set and reports itself as
// truncated.
const MaxCapturedFiles = 200
