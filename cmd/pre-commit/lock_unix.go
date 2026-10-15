//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// acquireLock tries to get an exclusive file lock keyed to the current repo.
// Returns the lock file on success, or an error if another instance holds the lock.
func acquireLock() (*os.File, error) {
	lockPath := getLockPath()

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	// Non-blocking exclusive lock
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock already held: %w", err)
	}

	return f, nil
}

// releaseLock releases and removes the lock file.
func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
}
