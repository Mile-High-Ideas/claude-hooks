package commitqueue

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// writeNewJSON writes v to path, failing if the file already exists. The
// exclusive create is what stops two processes claiming one slot number.
func writeNewJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("commitqueue: encode %s: %w", filepath.Base(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("commitqueue: create %s: %w", filepath.Base(path), err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("commitqueue: write %s: %w", filepath.Base(path), err)
	}
	return f.Close()
}

// readJSONDir decodes every *.json file in dir into a T. A file that
// vanished or is unreadable mid-listing is skipped: another process may be
// removing it, and a half-written one is never there because writers create
// exclusively and write in one call.
func readJSONDir[T any](dir string) ([]T, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("commitqueue: list %s: %w", dir, err)
	}
	var out []T
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var v T
		if json.Unmarshal(data, &v) != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// tickets returns every waiter, oldest first. Ties on arrival time (two
// callers in the same nanosecond) break on the ticket id so every process
// computes the same order.
func (q *Queue) tickets() ([]Ticket, error) {
	ts, err := readJSONDir[Ticket](q.ticketDir())
	if err != nil {
		return nil, err
	}
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].QueuedAt != ts[j].QueuedAt {
			return ts[i].QueuedAt < ts[j].QueuedAt
		}
		return ts[i].ID < ts[j].ID
	})
	return ts, nil
}

// heldSlots returns every held slot by slot number.
func (q *Queue) heldSlots() ([]Slot, error) {
	ss, err := readJSONDir[Slot](q.slotDir())
	if err != nil {
		return nil, err
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].Number < ss[j].Number })
	return ss, nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("commitqueue: remove %s: %w", filepath.Base(path), err)
	}
	return nil
}
