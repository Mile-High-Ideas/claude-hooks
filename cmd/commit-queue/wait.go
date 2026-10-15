package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// finishedRun is one exit file written after a wait began.
type finishedRun struct {
	Name string
	Code string
}

// waitForExits blocks until at least one run in the watched directories
// writes its exit file after the wait began, then prints "<name>: <code>"
// for each. It compares modification times, not file counts, because
// re-queueing a commit deletes its old exit file first.
func waitForExits(all bool, poll time.Duration, out io.Writer) int {
	q, _, repo, err := openQueue(".", 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	pattern := filepath.Join(runsDir(q, repo), "*.exit")
	if all {
		pattern = filepath.Join(q.Dir(), "runs", "*", "*.exit")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runs, err := waitForNewExits(ctx, pattern, time.Now(), poll)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, r := range runs {
		say(out, "%s: %s\n", r.Name, r.Code)
	}
	return 0
}

// waitForNewExits polls pattern until some file matches with a
// modification time after since.
func waitForNewExits(ctx context.Context, pattern string, since time.Time, poll time.Duration) ([]finishedRun, error) {
	for {
		runs, err := exitsSince(pattern, since)
		if err != nil || len(runs) > 0 {
			return runs, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func exitsSince(pattern string, since time.Time) ([]finishedRun, error) {
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("bad pattern %s: %w", pattern, err)
	}
	var runs []finishedRun
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.ModTime().After(since) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		runs = append(runs, finishedRun{
			Name: strings.TrimSuffix(filepath.Base(p), ".exit"),
			Code: strings.TrimSpace(string(data)),
		})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Name < runs[j].Name })
	return runs, nil
}
