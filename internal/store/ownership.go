package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrStoreOwned is returned when a task database is already owned by another
// process. Ownership is what makes "the State Store is stopped" a fact an
// offline command can check rather than a rule the operator is trusted to
// follow: overwriting a database a running server still holds leaves it
// writing to an unlinked inode while the store serves a file nobody owns.
var ErrStoreOwned = errors.New("task database is owned by another process")

// Ownership is one process's exclusive claim over one task database, held for
// as long as that process owns the file. The serving State Store takes it for
// its whole life; the offline recovery commands take it for the duration of an
// operation that rewrites the file.
//
// The claim lives in a sidecar file beside the database rather than on the
// database inode itself, so it survives the rename a restore performs: after
// the swap the sidecar is still the one path every process locks.
type Ownership struct {
	f *os.File
}

// AcquireOwnership takes the store's ownership lock without blocking. A
// database another process owns comes back as ErrStoreOwned, never as a wait:
// a process that must not proceed has to hear so, and a blocking lock would
// turn a mistyped command into a hung terminal.
func AcquireOwnership(path string) (*Ownership, error) {
	lockPath := ownershipLockPath(path)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create store directory for %s: %w", lockPath, err)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", lockPath, err)
	}
	taken, err := lockFile(f)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", lockPath, err), f.Close())
	}
	if !taken {
		return nil, errors.Join(fmt.Errorf("%s: %w", path, ErrStoreOwned), f.Close())
	}
	return &Ownership{f: f}, nil
}

// Release drops the claim and closes the sidecar. Closing the descriptor alone
// would release it too; the explicit unlock keeps the intent readable and
// makes Release idempotent.
func (o *Ownership) Release() error {
	if o == nil || o.f == nil {
		return nil
	}
	err := unlockFile(o.f)
	f := o.f
	o.f = nil
	return errors.Join(err, f.Close())
}

// ownershipLockPath is the sidecar every owner of path locks. It is derived
// from the database path, so the State Store process and an offline command
// that were given the same database agree on it without configuration.
func ownershipLockPath(path string) string { return path + ".lock" }
