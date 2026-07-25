package memory

// ── Backup paths interface ─────────────────────────────────────────────────
//
// Providers that store data outside HERMES_HOME implement BackupProvider so
// the backup/restore workflow can include those external paths.

// BackupProvider is an optional interface for providers that store data
// outside the main HERMES_HOME directory tree. Examples include external
// SQLite databases, vector-store indexes, or key-value stores that live
// at well-known paths elsewhere on the filesystem.
type BackupProvider interface {
	// BackupPaths returns absolute file or directory paths that should be
	// included in backup/restore workflows. Return an empty slice when the
	// provider has no external data to back up.
	BackupPaths() []string
}
