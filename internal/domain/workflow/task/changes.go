package task

// ChangeStats is what one attempt changed, measured at commit or push time.
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

// MaxCapturedFiles caps the file entries one capture stores; totals still
// cover all files.
const MaxCapturedFiles = 200
