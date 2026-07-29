package config

import "path/filepath"

// IndexingConfig locates the workspace codesearch index.
//
// The index is a large, fully rebuildable artifact, so both paths are
// settable to allow putting it on different storage from the task work tree.
type IndexingConfig struct {
	// IndexDir holds the codesearch sidecar files.
	IndexDir string `toml:"index_dir" yaml:"index_dir" json:"index_dir,omitempty"`
	// DBPath is the SQLite database holding index lifecycle metadata.
	DBPath string `toml:"db_path" yaml:"db_path" json:"db_path,omitempty"`
}

// WithDefaults fills unset paths from the daemon's work directory.
//
// Deriving from workDir rather than a shared data home is deliberate: two
// archied instances run on one host, each with its own work_dir, so this
// keeps their indexes separate without either having to configure anything.
func (c IndexingConfig) WithDefaults(workDir string) IndexingConfig {
	if c.IndexDir == "" {
		c.IndexDir = filepath.Join(workDir, "indexes")
	}
	if c.DBPath == "" {
		c.DBPath = filepath.Join(workDir, "workspace-indexes.db")
	}
	return c
}
