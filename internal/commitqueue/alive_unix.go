//go:build !windows

package commitqueue

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with this id exists. Signal 0
// checks without delivering anything; EPERM means it exists but belongs to
// another user, which still counts as alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
