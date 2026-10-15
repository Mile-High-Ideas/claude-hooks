package queuedash

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
)

const testRepo = "/code/app/.git"

// queueFixture is a commit queue in a temporary directory, with every
// process alive unless listed in dead.
type queueFixture struct {
	t     *testing.T
	queue *commitqueue.Queue
	dead  map[int]bool
}

func newQueueFixture(t *testing.T) *queueFixture {
	t.Helper()
	f := &queueFixture{t: t, dead: map[int]bool{}}
	q, err := commitqueue.New(commitqueue.Options{
		Dir:          t.TempDir(),
		Slots:        2,
		Poll:         time.Millisecond,
		ProcessAlive: f.alive,
	})
	if err != nil {
		t.Fatalf("commitqueue.New = %v", err)
	}
	f.queue = q
	return f
}

func (f *queueFixture) alive(pid int) bool { return pid > 0 && !f.dead[pid] }

func (f *queueFixture) hold(name string, pid int) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := commitqueue.Caller{Name: name, Repo: testRepo, PID: pid, WaiterPID: pid}
	if _, err := f.queue.Acquire(ctx, c, nil, nil); err != nil {
		f.t.Fatalf("Acquire(%s) = %v", name, err)
	}
}

// writeRun writes a run log, and an exit file when code is not nil.
func (f *queueFixture) writeRun(name, text string, code *int) {
	f.t.Helper()
	dir := filepath.Join(f.queue.Dir(), "runs", commitqueue.RepoKey(testRepo))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".log"), []byte(text), 0o644); err != nil {
		f.t.Fatalf("write log: %v", err)
	}
	if code == nil {
		return
	}
	// The exit file is written after the log's last write.
	later := time.Now().Add(time.Second)
	exit := filepath.Join(dir, name+".exit")
	if err := os.WriteFile(exit, []byte(fmt.Sprintf("%d\n", *code)), 0o644); err != nil {
		f.t.Fatalf("write exit: %v", err)
	}
	if err := os.Chtimes(exit, later, later); err != nil {
		f.t.Fatalf("chtimes: %v", err)
	}
}

func runLog(name string, pid int, queuedAt time.Time, finished bool) string {
	clock := func(d time.Duration) string { return queuedAt.Add(d).Format("15:04:05") }
	lines := []string{
		fmt.Sprintf("[run-commit] queued at %d pid %d on feat/x in /code/app", queuedAt.Unix(), pid),
		"[run-commit] subject: feat: x",
		fmt.Sprintf("[commit-slot] %s holds slot-1 from %s", name, clock(time.Second)),
		fmt.Sprintf("  ▶ %s Lint", clock(2*time.Second)),
	}
	if finished {
		lines = append(lines,
			fmt.Sprintf("  ✅ Lint (%s, 10s)", clock(12*time.Second)),
			"[feat/x abc1234] feat: x",
			fmt.Sprintf("[commit-slot] %s released slot-1", name),
		)
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestCollectorReadsRunsAndStoresFinishedOnes(t *testing.T) {
	f := newQueueFixture(t)
	queued := time.Now().Add(-time.Minute)
	zero := 0
	f.writeRun("done", runLog("done", 4242, queued, true), &zero)
	f.writeRun("busy", runLog("busy", os.Getpid(), queued, false), nil)
	f.hold("busy", os.Getpid())

	history, err := OpenHistory(filepath.Join(t.TempDir(), "history.jsonl"))
	if err != nil {
		t.Fatalf("OpenHistory = %v", err)
	}
	c := NewCollector(f.queue, history, f.alive)
	scan, err := c.Collect(time.Now())
	if err != nil {
		t.Fatalf("Collect = %v", err)
	}
	states := map[string]RunState{}
	for _, r := range scan.Runs {
		states[r.Name] = r.State
		if r.Repo != "app" || r.RepoKey != commitqueue.RepoKey(testRepo) {
			t.Errorf("run %s repo = %q/%q, want app", r.Name, r.Repo, r.RepoKey)
		}
	}
	if states["done"] != StateFinished || states["busy"] != StateRunning {
		t.Errorf("states = %v, want done finished and busy running", states)
	}
	if scan.Ingested != 1 || history.Counts().Runs != 1 {
		t.Errorf("ingested %d, history %d runs; want the finished run stored once", scan.Ingested, history.Counts().Runs)
	}
	if len(scan.Slots) != 1 || scan.Slots[0].Owner != "busy" || scan.Slots[0].Repo != "app" {
		t.Errorf("slots = %+v, want busy holding one", scan.Slots)
	}

	again, err := c.Collect(time.Now())
	if err != nil || again.Ingested != 0 {
		t.Errorf("second Collect ingested %d (%v), want 0", again.Ingested, err)
	}
}

func TestCollectorSkipsALogItCannotRead(t *testing.T) {
	f := newQueueFixture(t)
	zero := 0
	f.writeRun("good", runLog("good", 4242, time.Now().Add(-time.Minute), true), &zero)
	corrupt := runLog("corrupt", 4243, time.Now().Add(-time.Minute), false) + "  ▶ 25:61:00 Lint\n"
	f.writeRun("corrupt", corrupt, &zero)
	history, err := OpenHistory(filepath.Join(t.TempDir(), "history.jsonl"))
	if err != nil {
		t.Fatalf("OpenHistory = %v", err)
	}
	scan, err := NewCollector(f.queue, history, f.alive).Collect(time.Now())
	if err != nil {
		t.Fatalf("Collect = %v", err)
	}
	if len(scan.Runs) != 1 || scan.Runs[0].Name != "good" {
		t.Errorf("runs = %+v, want only the readable log", scan.Runs)
	}
}

func TestCollectorIgnoresAnExitFileOlderThanTheLog(t *testing.T) {
	f := newQueueFixture(t)
	one := 1
	f.writeRun("requeued", runLog("requeued", os.Getpid(), time.Now(), false), &one)
	// A re-queued run rewrote its log after the old exit file.
	dir := filepath.Join(f.queue.Dir(), "runs", commitqueue.RepoKey(testRepo))
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "requeued.exit"), past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	history, err := OpenHistory(filepath.Join(t.TempDir(), "history.jsonl"))
	if err != nil {
		t.Fatalf("OpenHistory = %v", err)
	}
	scan, err := NewCollector(f.queue, history, f.alive).Collect(time.Now())
	if err != nil {
		t.Fatalf("Collect = %v", err)
	}
	if len(scan.Runs) != 1 || scan.Runs[0].State == StateFinished {
		t.Errorf("runs = %+v, want the re-queued run unfinished", scan.Runs)
	}
}

func TestDashboardServesThePageAndState(t *testing.T) {
	f := newQueueFixture(t)
	zero := 0
	f.writeRun("done", runLog("done", 4242, time.Now().Add(-time.Minute), true), &zero)
	f.hold("manual", os.Getpid())
	history, err := OpenHistory(filepath.Join(t.TempDir(), "history.jsonl"))
	if err != nil {
		t.Fatalf("OpenHistory = %v", err)
	}
	dash := NewDashboard(NewCollector(f.queue, history, f.alive), history, 2, 20)
	srv := httptest.NewServer(dash.Handler())
	defer srv.Close()

	page := dashGet(t, srv.URL+"/")
	if !strings.Contains(page, `<select id="window" data-default="20">`) {
		t.Error("page does not carry the default window")
	}
	var state DashboardState
	if err := json.Unmarshal([]byte(dashGet(t, srv.URL+"/api/state?window=5000")), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if len(state.Recent) != 1 || state.Recent[0].Name != "done" {
		t.Errorf("recent = %+v, want the finished run", state.Recent)
	}
	if len(state.HeldWithoutRun) != 1 || state.HeldWithoutRun[0].Owner != "manual" {
		t.Errorf("heldWithoutRun = %+v, want the acquire-held slot", state.HeldWithoutRun)
	}
	if resp, err := http.Get(srv.URL + "/nope"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /nope = %v, %v; want 404", resp, err)
	}
}

func TestClampWindow(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{{"", 50}, {"junk", 50}, {"0", 50}, {"20", 20}, {"5000", 1000}}
	for _, tt := range tests {
		if got := clampWindow(tt.raw, 50); got != tt.want {
			t.Errorf("clampWindow(%q) = %d, want %d", tt.raw, got, tt.want)
		}
	}
}

func dashGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}
