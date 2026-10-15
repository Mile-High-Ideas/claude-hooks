//go:build !windows

package commitqueue

import (
	"fmt"
	"os"
	"syscall"
)

// withLock runs fn while holding an exclusive lock on the queue's lock file,
// waiting for any other holder. The file is never removed: deleting a file
// others may be blocked on would let two processes hold "the" lock at once.
func (q *Queue) withLock(fn func() error) error {
	f, err := os.OpenFile(q.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("commitqueue: open lock: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("commitqueue: lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
