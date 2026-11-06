package queuedash

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Errors RemoveAbandoned returns when it refuses to remove a run.
var (
	ErrInvalidRunName = errors.New("invalid run name")
	ErrRunNotFound    = errors.New("no such run")
	ErrNotAbandoned   = errors.New("run is not abandoned")
	ErrRunHoldsSlot   = errors.New("run still holds a slot; release it first")
)

// RemoveAbandoned deletes the log, and any exit file, of the run name in the
// repository directory repoKey, so it leaves the Abandoned list. It reads
// the run again first and refuses unless it is still abandoned and its name
// holds no slot: `run` recreates the log when a name is queued again, and
// that new run must not be deleted from under it.
func (c *Collector) RemoveAbandoned(repoKey, name string, now time.Time) error {
	if !isPlainFileName(repoKey) || !isPlainFileName(name) {
		return ErrInvalidRunName
	}
	dir := filepath.Join(c.queue.Dir(), "runs", repoKey)
	logPath := filepath.Join(dir, name+".log")
	if _, err := os.Stat(logPath); err != nil {
		return ErrRunNotFound
	}
	snap, err := c.queue.Snapshot()
	if err != nil {
		return fmt.Errorf("read queue: %w", err)
	}
	holders := map[string]bool{}
	for _, s := range queueSlots(snap) {
		holders[s.RepoKey+"/"+s.Owner] = true
	}
	if holders[repoKey+"/"+name] {
		return ErrRunHoldsSlot
	}
	run, ok := c.readRun(dir, repoKey, name, now, holders)
	if !ok {
		return ErrRunNotFound
	}
	if run.State != StateAbandoned {
		return ErrNotAbandoned
	}
	return c.deleteRunFiles(logPath, filepath.Join(dir, name+".exit"))
}

// deleteRunFiles removes a run's log and exit file and forgets its cache.
func (c *Collector) deleteRunFiles(logPath, exitPath string) error {
	if err := os.Remove(logPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", logPath, err)
	}
	if err := os.Remove(exitPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", exitPath, err)
	}
	delete(c.finished, logPath)
	return nil
}

// isPlainFileName reports whether s names an entry directly inside a
// directory: not empty, not hidden, and with no path separator or "..".
func isPlainFileName(s string) bool {
	return s != "" && s[0] != '.' && filepath.Base(s) == s && filepath.IsLocal(s)
}
