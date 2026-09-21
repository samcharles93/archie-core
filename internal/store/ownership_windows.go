//go:build windows

// flock is a POSIX primitive and Windows is not a supported deployment surface
// (CLAUDE.md's deployment model is Linux and macOS), so this file fails closed:
// every lock attempt reports an error rather than silently degrading to "no
// lock". An offline command that cannot prove it owns the database must refuse
// to rewrite it, which is the only safe reading of an unavailable primitive.
//
// If a Windows deployment ever becomes supported, swap this for a real
// LockFileEx/UnlockFileEx pair; nothing else in the package needs to change.

package store

import (
	"errors"
	"os"
)

func lockFile(*os.File) (bool, error) {
	return false, errors.New("store ownership locking requires a POSIX flock, which this platform does not provide")
}

func unlockFile(*os.File) error { return nil }
