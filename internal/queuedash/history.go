package queuedash

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// maxHistoryLine bounds one stored run. A run with a few dozen steps is a
// few kilobytes; this leaves room for pathological logs.
const maxHistoryLine = 4 << 20

// History is the timing history: every finished run, one JSON object per
// line, appended as runs finish. A run stored again (the dashboard re-reads
// every finished log when it starts) is appended only if it changed, and on
// reading the last copy of an id wins. Several dashboards may share a file;
// each appends whole lines in one write.
type History struct {
	path string
	mu   sync.Mutex
	runs map[string]Run
	// stored holds each run's last written encoding, to skip rewrites.
	stored map[string][]byte
}

// OpenHistory loads the history at path, creating its directory. A missing
// file is an empty history; an undecodable line is skipped.
func OpenHistory(path string) (*History, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create history directory: %w", err)
	}
	h := &History{path: path, runs: map[string]Run{}, stored: map[string][]byte{}}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open history: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxHistoryLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		var run Run
		if len(line) == 0 || json.Unmarshal(line, &run) != nil || run.ID == "" {
			continue
		}
		h.runs[run.ID] = run
		h.stored[run.ID] = append([]byte(nil), line...)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	return h, nil
}

// Upsert stores a finished run and reports whether the history did not
// hold it before.
func (h *History) Upsert(run Run) (bool, error) {
	if run.State != StateFinished {
		return false, fmt.Errorf("only finished runs are stored, got %s", run.State)
	}
	line, err := json.Marshal(run)
	if err != nil {
		return false, fmt.Errorf("encode run %s: %w", run.ID, err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	prev, existed := h.stored[run.ID]
	if existed && bytes.Equal(prev, line) {
		return false, nil
	}
	if err := h.appendLine(line); err != nil {
		return false, err
	}
	h.runs[run.ID] = run
	h.stored[run.ID] = line
	return !existed, nil
}

func (h *History) appendLine(line []byte) error {
	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open history for append: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("append to history: %w", err)
	}
	return f.Close()
}

// RecentRuns returns the newest limit runs by finish time.
func (h *History) RecentRuns(limit int) []Run {
	return h.newest(limit, func(Run) bool { return true })
}

// RecentSlotRuns returns the newest limit runs that took a slot, which is
// what the statistics window covers.
func (h *History) RecentSlotRuns(limit int) []Run {
	return h.newest(limit, func(r Run) bool { return r.SlotAcquiredAt != nil })
}

func (h *History) newest(limit int, keep func(Run) bool) []Run {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Run, 0, len(h.runs))
	for _, r := range h.runs {
		if keep(r) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := finishedAtOrZero(out[i]), finishedAtOrZero(out[j])
		if a != b {
			return a > b
		}
		return out[i].ID < out[j].ID
	})
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Counts reports how many runs and steps the history holds.
func (h *History) Counts() HistoryCounts {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := HistoryCounts{Runs: len(h.runs)}
	for _, r := range h.runs {
		c.Steps += len(r.Steps)
	}
	return c
}

func finishedAtOrZero(r Run) int64 {
	if r.FinishedAt == nil {
		return 0
	}
	return *r.FinishedAt
}
