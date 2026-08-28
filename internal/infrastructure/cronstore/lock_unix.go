//go:build unix

package cronstore

import (
	"fmt"
	"os"
	"syscall"
)

// lockedFile is an OS-level flock wrapper around a sidecar file. The
// store acquires it before reading or writing the JSON, so a CLI edit
// and a daemon writer cannot tear the same jobs.json.
//
// flock is per-open-file-description, which means a single Store holds
// the lock for as long as it lives; concurrent processes opening the
// same sidecar queue behind it.
type lockedFile struct {
	f *os.File
}

// openLock creates (or opens) the lock file and returns it unlocked.
// The lock is acquired separately via Lock, so Open can hydrate without
// holding it.
func openLock(path string) (*lockedFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	return &lockedFile{f: f}, nil
}

// Lock blocks until the OS-level exclusive flock is acquired. flock is
// reentrant on Linux per-fd, so calling Lock twice on the same lockedFile
// from the same goroutine is fine; calling it from two goroutines still
// serialises.
func (l *lockedFile) Lock() error {
	if l == nil || l.f == nil {
		return fmt.Errorf("cronstore: lock not initialised")
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock LOCK_EX: %w", err)
	}
	return nil
}

// Unlock releases the exclusive flock. Idempotent in the sense that a
// second call after Close simply returns nil because the fd is closed.
func (l *lockedFile) Unlock() error {
	if l == nil || l.f == nil {
		return nil
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("flock LOCK_UN: %w", err)
	}
	return nil
}

// Close closes the sidecar fd. The flock is released automatically by
// the kernel.
func (l *lockedFile) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
