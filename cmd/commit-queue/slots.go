package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
)

// acquireSlot blocks until name holds a slot, then exits leaving it held for
// ownerPID (the caller's shell by default) until `release` or until that
// process exits. It is for work that is not a single git commit.
func acquireSlot(name string, ownerPID, slotsFlag int, out io.Writer) int {
	if err := validName(name); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	q, _, repo, err := openQueue(".", slotsFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if ownerPID <= 0 {
		ownerPID = os.Getppid()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	caller := commitqueue.Caller{Name: name, Repo: repo.CommonDir, PID: ownerPID}
	slot, err := q.Acquire(ctx, caller,
		func(position int) { say(out, "[commit-queue] %s waiting, position %d of the line\n", name, position) },
		func(r commitqueue.Reclaimed) { logReclaim(out, r) },
	)
	if err != nil {
		say(os.Stderr, "[commit-queue] %s: %v\n", name, err)
		return 1
	}
	say(out, "[commit-slot] %s holds slot-%d from %s (owner pid %d)\n", name, slot.Number, time.Unix(0, slot.StartedAt).Format("15:04:05"), ownerPID)
	return 0
}

// releaseSlot frees whatever name holds or is waiting for in this repo.
func releaseSlot(name string, out io.Writer) int {
	q, _, repo, err := openQueue(".", 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	freed, err := q.Release(name, repo.CommonDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, n := range freed {
		say(out, "[commit-slot] %s released slot-%d\n", name, n)
	}
	return 0
}

// printStatus lists holders and waiters in the order they will be served.
func printStatus(asJSON bool, out io.Writer) int {
	q, _, _, err := openQueue(".", 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	snap, err := q.Snapshot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Dir string `json:"dir"`
			commitqueue.Snapshot
		}{q.Dir(), snap}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	writeStatusText(out, q.Dir(), snap, time.Now())
	return 0
}

func writeStatusText(out io.Writer, dir string, snap commitqueue.Snapshot, now time.Time) {
	say(out, "%d of %d slots held · %d waiting · %s\n", len(snap.Held), snap.Slots, len(snap.Waiting), dir)
	for _, s := range snap.Held {
		say(out, "  slot-%d  %-28s %-24s %s\n", s.Number, s.Name, repoLabel(s.Repo), roundDur(now.Sub(time.Unix(0, s.StartedAt))))
	}
	for i, t := range snap.Waiting {
		say(out, "  #%-5d %-28s %-24s waiting %s\n", i+1, t.Name, repoLabel(t.Repo), roundDur(now.Sub(time.Unix(0, t.QueuedAt))))
	}
}

// repoLabel shortens a git common directory to the repository's name.
func repoLabel(commonDir string) string {
	if filepath.Base(commonDir) == ".git" {
		return filepath.Base(filepath.Dir(commonDir))
	}
	return filepath.Base(commonDir)
}

func roundDur(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d.Round(time.Second)
}
