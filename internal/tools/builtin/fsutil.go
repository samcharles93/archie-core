// Lifted from tau internal/agent/tools/fsutil.go
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
	"context"
	"os"
	"path/filepath"
)

// runWithContext runs fn in a goroutine and returns its result, or ctx.Err()
// if ctx is done first. Go cannot forcibly cancel a blocked syscall (e.g. a
// hung NFS/FUSE mount), so fn's goroutine is not killed - it keeps running
// in the background and its result is discarded - but the caller stops
// waiting on it, so a hung filesystem can no longer block the tool executor
// (and therefore the coordinator's turn loop) indefinitely.
//
// If the context is already done when runWithContext is called, fn is never
// spawned. This avoids a goroutine leak that can race with test cleanup.
func runWithContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// writeFileAtomic replaces path atomically using a temp file in the same directory.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	defer cleanup()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
