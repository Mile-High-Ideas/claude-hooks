package queuedash

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
)

const deadPID = 9001

// newRemoveFixture writes one run of each state the Abandoned list cares
// about and returns the queue and a collector over it.
func newRemoveFixture(t *testing.T) (*queueFixture, *Collector) {
	t.Helper()
	f := newQueueFixture(t)
	queued := time.Now().Add(-time.Hour)
	zero := 0
	f.writeRun("gone", runLog("gone", deadPID, queued, false), nil)
	f.writeRun("busy", runLog("busy", os.Getpid(), queued, false), nil)
	f.hold("busy", os.Getpid())
	f.writeRun("done", runLog("done", deadPID, queued, true), &zero)
	f.writeRun("stuck", runLog("stuck", deadPID+1, queued, false), nil)
	f.hold("stuck", deadPID+1)
	f.dead[deadPID] = true
	f.dead[deadPID+1] = true
	history, err := OpenHistory(filepath.Join(t.TempDir(), "history.jsonl"))
	if err != nil {
		t.Fatalf("OpenHistory = %v", err)
	}
	return f, NewCollector(f.queue, history, f.alive)
}

func (f *queueFixture) logExists(name string) bool {
	_, err := os.Stat(filepath.Join(f.queue.Dir(), "runs", commitqueue.RepoKey(testRepo), name+".log"))
	return err == nil
}

func TestRemoveAbandoned(t *testing.T) {
	key := commitqueue.RepoKey(testRepo)
	tests := []struct {
		name    string
		repoKey string
		run     string
		want    error
	}{
		{"abandoned run is removed", key, "gone", nil},
		{"running run is refused", key, "busy", ErrRunHoldsSlot},
		{"finished run is refused", key, "done", ErrNotAbandoned},
		{"dead run still holding a slot is refused", key, "stuck", ErrRunHoldsSlot},
		{"unknown run", key, "missing", ErrRunNotFound},
		{"unknown repo", "other-00000000", "gone", ErrRunNotFound},
		{"path in name", key, "../gone", ErrInvalidRunName},
		{"path in repo", "../runs", "gone", ErrInvalidRunName},
		{"hidden name", key, ".gone", ErrInvalidRunName},
		{"empty name", key, "", ErrInvalidRunName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c := newRemoveFixture(t)
			err := c.RemoveAbandoned(tt.repoKey, tt.run, time.Now())
			if !errors.Is(err, tt.want) {
				t.Fatalf("RemoveAbandoned(%q, %q) = %v, want %v", tt.repoKey, tt.run, err, tt.want)
			}
			removed := !f.logExists("gone")
			if removed != (tt.want == nil) {
				t.Errorf("gone.log removed = %v, want %v", removed, tt.want == nil)
			}
			for _, kept := range []string{"busy", "done", "stuck"} {
				if !f.logExists(kept) {
					t.Errorf("%s.log was removed", kept)
				}
			}
		})
	}
}

func TestRemoveAbandonedDropsAStrayExitFile(t *testing.T) {
	f, c := newRemoveFixture(t)
	dir := filepath.Join(f.queue.Dir(), "runs", commitqueue.RepoKey(testRepo))
	// An exit file older than the log belongs to an earlier run of the name.
	exit := filepath.Join(dir, "gone.exit")
	if err := os.WriteFile(exit, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(exit, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := c.RemoveAbandoned(commitqueue.RepoKey(testRepo), "gone", time.Now()); err != nil {
		t.Fatalf("RemoveAbandoned = %v", err)
	}
	if _, err := os.Stat(exit); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gone.exit still exists (stat err %v)", err)
	}
}
