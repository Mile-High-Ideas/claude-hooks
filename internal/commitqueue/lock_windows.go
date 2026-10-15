//go:build windows

package commitqueue

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32.NewProc("LockFileEx")
	unlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const lockfileExclusiveLock = 0x00000002

// withLock runs fn while holding an exclusive lock on the queue's lock file,
// waiting for any other holder. The file is never removed: deleting a file
// others may be blocked on would let two processes hold "the" lock at once.
func (q *Queue) withLock(fn func() error) error {
	f, err := os.OpenFile(q.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("commitqueue: open lock: %w", err)
	}
	defer func() { _ = f.Close() }()
	handle := syscall.Handle(f.Fd())
	ol := new(syscall.Overlapped)
	if r, _, err := lockFileEx.Call(uintptr(handle), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(ol))); r == 0 {
		return fmt.Errorf("commitqueue: lock: %w", err)
	}
	defer func() {
		ol := new(syscall.Overlapped)
		_, _, _ = unlockFileEx.Call(uintptr(handle), 0, 1, 0, uintptr(unsafe.Pointer(ol)))
	}()
	return fn()
}
