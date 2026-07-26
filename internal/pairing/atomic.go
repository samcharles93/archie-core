package pairing

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path by writing to a temporary file in
// the same directory and renaming it into place. A rename within the
// same filesystem is atomic, so a reader never observes a partially
// written file, and a crash mid-write leaves the original file (or no
// file) intact rather than a corrupt one. The file is created with mode
// 0600  --  pairing state (codes, approvals, rate-limit history) is
// sensitive and must not be group/world readable.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("pairing: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	// Clean up the temp file on any failure path; a successful rename
	// removes the source, so os.Remove no-ops (ENOENT, discarded) on the
	// success path.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pairing: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("pairing: close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("pairing: chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("pairing: rename into place: %w", err)
	}
	return nil
}
