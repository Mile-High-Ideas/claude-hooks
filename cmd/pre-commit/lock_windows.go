//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx    = kernel32.NewProc("LockFileEx")
	unlockFileEx  = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// acquireLock tries to get an exclusive file lock keyed to the current repo.
// Returns the lock file on success, or an error if another instance holds the lock.
func acquireLock() (*os.File, error) {
	lockPath := getLockPath()

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	// Non-blocking exclusive lock via LockFileEx
	handle := syscall.Handle(f.Fd())
	ol := new(syscall.Overlapped)
	r1, _, err := lockFileEx.Call(
		uintptr(handle),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		1, 0,
		uintptr(unsafe.Pointer(ol)),
	)
	if r1 == 0 {
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
	handle := syscall.Handle(f.Fd())
	ol := new(syscall.Overlapped)
	_, _, _ = unlockFileEx.Call(
		uintptr(handle),
		0,
		1, 0,
		uintptr(unsafe.Pointer(ol)),
	)
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
}
