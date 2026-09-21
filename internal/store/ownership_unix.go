//go:build unix

package store

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes a non-blocking exclusive flock on f. The boolean reports
// whether it was taken: a lock another process holds (EWOULDBLOCK) is a normal
// answer to "is the State Store running?", not an error.
func lockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func unlockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return err
	}
	return nil
}
