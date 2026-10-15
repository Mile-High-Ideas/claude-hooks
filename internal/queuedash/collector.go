package queuedash

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
)

// Collector reads the queue's state directory and keeps the history up to
// date:
//
//	<state>/slots, <state>/tickets     the queue (via commitqueue)
//	<state>/runs/<repo>/<name>.log     output so far
//	<state>/runs/<repo>/<name>.exit    exit code, written last
//
// Logs that have not changed since the last scan are not read again.
type Collector struct {
	queue   *commitqueue.Queue
	history *History
	alive   func(pid int) bool
	// finished caches runs whose log and exit file were unchanged, by path.
	finished map[string]cachedRun
	// stored remembers which finished runs this process already wrote.
	stored map[string]bool
}

type cachedRun struct {
	key string
	run Run
}

// Scan is one look at the queue.
type Scan struct {
	Runs    []Run
	Slots   []QueueSlot
	Tickets []QueueTicket
	// Ingested counts runs the history did not hold before this scan.
	Ingested int
}

// NewCollector reads q's state and stores finished runs in history.
// alive reports whether a runner process still exists; nil uses the real
// process table.
func NewCollector(q *commitqueue.Queue, history *History, alive func(pid int) bool) *Collector {
	if alive == nil {
		alive = commitqueue.ProcessAlive
	}
	return &Collector{
		queue:    q,
		history:  history,
		alive:    alive,
		finished: map[string]cachedRun{},
		stored:   map[string]bool{},
	}
}

// Collect scans the queue and stores runs that finished. The first scan of
// a process stores every finished log on disk, which backfills a new
// history; later scans store only runs that just finished.
func (c *Collector) Collect(now time.Time) (Scan, error) {
	snap, err := c.queue.Snapshot()
	if err != nil {
		return Scan{}, fmt.Errorf("read queue: %w", err)
	}
	scan := Scan{Slots: queueSlots(snap), Tickets: queueTickets(snap)}
	holders := map[string]bool{}
	for _, s := range scan.Slots {
		holders[s.RepoKey+"/"+s.Owner] = true
	}
	scan.Runs, err = c.readRuns(now, holders)
	if err != nil {
		return Scan{}, err
	}
	for _, run := range scan.Runs {
		if run.State != StateFinished || c.stored[run.ID] {
			continue
		}
		isNew, err := c.history.Upsert(run)
		if err != nil {
			return Scan{}, err
		}
		if isNew {
			scan.Ingested++
		}
		c.stored[run.ID] = true
	}
	return scan, nil
}

func (c *Collector) readRuns(now time.Time, holders map[string]bool) ([]Run, error) {
	repoDirs, err := os.ReadDir(filepath.Join(c.queue.Dir(), "runs"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list run directories: %w", err)
	}
	var runs []Run
	for _, d := range repoDirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(c.queue.Dir(), "runs", d.Name())
		names, err := logNames(dir)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if run, ok := c.readRun(dir, d.Name(), name, now, holders); ok {
				runs = append(runs, run)
			}
		}
	}
	return runs, nil
}

func logNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasSuffix(n, ".log") && !strings.HasPrefix(n, ".") {
			names = append(names, strings.TrimSuffix(n, ".log"))
		}
	}
	return names, nil
}

// readRun builds one run, from cache when its files have not changed.
func (c *Collector) readRun(dir, repoKey, name string, now time.Time, holders map[string]bool) (Run, bool) {
	logPath := filepath.Join(dir, name+".log")
	exitPath := filepath.Join(dir, name+".exit")
	logStat, err := os.Stat(logPath)
	if err != nil {
		return Run{}, false
	}
	exitStat, _ := os.Stat(exitPath)
	key := filesKey(logStat, exitStat)
	if cached, ok := c.finished[logPath]; ok && cached.key == key {
		return cached.run, true
	}
	text, exit, ok := readLogFiles(logPath, exitPath, exitStat)
	if !ok {
		return Run{}, false
	}
	run, ok := buildRunOrSkip(RunInput{
		Name:          name,
		RepoKey:       repoKey,
		Repo:          labelFromKey(repoKey),
		Text:          text,
		LogMtimeMs:    logStat.ModTime().UnixMilli(),
		Exit:          exit,
		SlotHeld:      holders[repoKey+"/"+name],
		NowMs:         now.UnixMilli(),
		IsRunnerAlive: c.alive,
	})
	if !ok {
		return Run{}, false
	}
	if exit != nil {
		c.finished[logPath] = cachedRun{key: key, run: run}
	}
	return run, true
}

// buildRunOrSkip builds a run, or reports false for a log BuildRun cannot
// read. A stamp the log pattern accepts but no clock shows, such as
// "25:61:00", makes BuildRun panic; one corrupt log must not take the whole
// dashboard down with it.
func buildRunOrSkip(in RunInput) (run Run, ok bool) {
	defer func() {
		if recover() != nil {
			run, ok = Run{}, false
		}
	}()
	return BuildRun(in), true
}

// readLogFiles reads a log and its exit code. `run` deletes both files and
// recreates the log when a name is queued again, so a reader can see an old
// exit file next to a new log. An exit file belongs to the log only if it
// was written no earlier than the log's last write (it is written last) and
// was not replaced while the log was read; otherwise the run counts as
// unfinished until a later scan.
func readLogFiles(logPath, exitPath string, exitStat fs.FileInfo) (string, *ExitInfo, bool) {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return "", nil, false
	}
	code, codeErr := -1, errors.New("no exit file")
	if exitStat != nil {
		code, codeErr = readExitCode(exitPath)
	}
	logAfter, err := os.Stat(logPath)
	if err != nil {
		return "", nil, false
	}
	exitAfter, _ := os.Stat(exitPath)
	paired := exitStat != nil && exitAfter != nil && codeErr == nil &&
		os.SameFile(exitStat, exitAfter) && exitAfter.ModTime().Equal(exitStat.ModTime()) &&
		!exitStat.ModTime().Before(logAfter.ModTime())
	if !paired {
		return string(data), nil, true
	}
	return string(data), &ExitInfo{Code: code, MtimeMs: exitStat.ModTime().UnixMilli()}, true
}

func readExitCode(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// filesKey identifies one version of a log and its exit file.
func filesKey(logStat, exitStat fs.FileInfo) string {
	key := fmt.Sprintf("%d:%d", logStat.ModTime().UnixNano(), logStat.Size())
	if exitStat != nil {
		key += fmt.Sprintf(":%d", exitStat.ModTime().UnixNano())
	}
	return key
}

// labelFromKey recovers the repo label from a commitqueue.RepoKey by
// dropping its hash suffix.
func labelFromKey(key string) string {
	if i := strings.LastIndex(key, "-"); i > 0 {
		return key[:i]
	}
	return key
}

func queueSlots(snap commitqueue.Snapshot) []QueueSlot {
	out := make([]QueueSlot, 0, len(snap.Held))
	for _, s := range snap.Held {
		out = append(out, QueueSlot{
			Slot:      s.Number,
			Owner:     s.Name,
			RepoKey:   commitqueue.RepoKey(s.Repo),
			Repo:      commitqueue.RepoLabel(s.Repo),
			StartedAt: s.StartedAt / int64(time.Millisecond),
		})
	}
	return out
}

func queueTickets(snap commitqueue.Snapshot) []QueueTicket {
	out := make([]QueueTicket, 0, len(snap.Waiting))
	for _, t := range snap.Waiting {
		out = append(out, QueueTicket{
			Name:     t.Name,
			RepoKey:  commitqueue.RepoKey(t.Repo),
			Repo:     commitqueue.RepoLabel(t.Repo),
			QueuedAt: t.QueuedAt / int64(time.Millisecond),
		})
	}
	return out
}
