package queuedash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dashI64(v int64) *int64 { return &v }

func finishedRun(id string, finishedAt int64, slotAcquired bool) Run {
	r := Run{
		ID:         id,
		Name:       id,
		State:      StateFinished,
		FinishedAt: dashI64(finishedAt),
		Steps:      []RunStep{{Name: "Lint", Result: StepPassed}},
	}
	if slotAcquired {
		r.SlotAcquiredAt = dashI64(finishedAt - 100)
	}
	return r
}

func TestHistoryStoresAndReloadsRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "history.jsonl")
	h, err := OpenHistory(path)
	if err != nil {
		t.Fatalf("OpenHistory = %v", err)
	}
	for _, r := range []Run{finishedRun("a", 1000, true), finishedRun("b", 3000, false), finishedRun("c", 2000, true)} {
		isNew, err := h.Upsert(r)
		if err != nil || !isNew {
			t.Fatalf("Upsert(%s) = %v, %v; want new", r.ID, isNew, err)
		}
	}

	reopened, err := OpenHistory(path)
	if err != nil {
		t.Fatalf("reopen = %v", err)
	}
	if got := dashIDs(reopened.RecentRuns(10)); got != "b,c,a" {
		t.Errorf("RecentRuns = %s, want newest first b,c,a", got)
	}
	if got := dashIDs(reopened.RecentSlotRuns(10)); got != "c,a" {
		t.Errorf("RecentSlotRuns = %s, want only runs that took a slot", got)
	}
	if got := dashIDs(reopened.RecentRuns(1)); got != "b" {
		t.Errorf("RecentRuns(1) = %s, want b", got)
	}
	if c := reopened.Counts(); c.Runs != 3 || c.Steps != 3 {
		t.Errorf("Counts = %+v, want 3 runs, 3 steps", c)
	}
}

func TestHistorySkipsAnUnchangedRunAndKeepsTheLatestCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	h, err := OpenHistory(path)
	if err != nil {
		t.Fatalf("OpenHistory = %v", err)
	}
	run := finishedRun("a", 1000, true)
	if _, err := h.Upsert(run); err != nil {
		t.Fatalf("Upsert = %v", err)
	}
	if isNew, err := h.Upsert(run); err != nil || isNew {
		t.Errorf("second Upsert = %v, %v; want not new", isNew, err)
	}
	if lines := lineCount(t, path); lines != 1 {
		t.Errorf("history has %d lines after an identical upsert, want 1", lines)
	}

	failure := "Lint web"
	run.Failure = &failure
	if _, err := h.Upsert(run); err != nil {
		t.Fatalf("Upsert changed = %v", err)
	}
	reopened, err := OpenHistory(path)
	if err != nil {
		t.Fatalf("reopen = %v", err)
	}
	got := reopened.RecentRuns(1)
	if len(got) != 1 || got[0].Failure == nil || *got[0].Failure != "Lint web" {
		t.Errorf("reloaded run = %+v, want the later copy", got)
	}
}

func TestHistoryRefusesUnfinishedRunsAndSkipsBadLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte("not json\n\n{\"id\":\"\"}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h, err := OpenHistory(path)
	if err != nil {
		t.Fatalf("OpenHistory = %v", err)
	}
	if c := h.Counts(); c.Runs != 0 {
		t.Errorf("Counts = %+v, want bad lines skipped", c)
	}
	if _, err := h.Upsert(Run{ID: "x", State: StateRunning}); err == nil {
		t.Error("Upsert of a running run = nil, want an error")
	}
}

func dashIDs(runs []Run) string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.ID
	}
	return strings.Join(out, ",")
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.Count(string(data), "\n")
}
