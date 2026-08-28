//go:build windows

// flock is a POSIX primitive; Windows does not implement syscall.Flock
// the same way. Windows deployments of archied are not the supported
// surface (the deployment model in CLAUDE.md and deployments/*.toml is
// Linux + macOS), so this file deliberately fails closed: every Lock
// returns an error rather than silently degrading to "no lock".
//
// If a Windows deployment ever becomes supported, swap this for a real
// LockFileEx / UnlockFileEx implementation; the rest of the store does
// not need to change.

package cronstore

import (
	"errors"
	"fmt"
	"os"
)

// lockedFile is a stub on Windows. The store still opens the sidecar
// file so the on-disk layout matches across platforms, but every Lock
// returns an explicit error.
type lockedFile struct {
	f *os.File
}

func openLock(path string) (*lockedFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	return &lockedFile{f: f}, nil
}

// errLockUnsupported is wrapped in every Lock call so callers can match
// on it without parsing strings.
var errLockUnsupported = errors.New("cronstore: cross-process locking is unsupported on Windows")

func (l *lockedFile) Lock() error   { return errLockUnsupported }
func (l *lockedFile) Unlock() error { return nil }

func (l *lockedFile) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Keep imports alive when this file is built in isolation.
var _ = fmt.Sprintf
