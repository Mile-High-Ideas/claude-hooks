package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
)

func TestCommitHeader(t *testing.T) {
	msgFile := filepath.Join(t.TempDir(), "msg.txt")
	writeFile(t, msgFile, "fix: from a file\n\nbody\n")
	tests := []struct {
		name   string
		args   []string
		want   string
		wantOK bool
	}{
		{"-m", []string{"-m", "feat: x\n\nbody"}, "feat: x", true},
		{"-mvalue", []string{"-mfeat: y"}, "feat: y", true},
		{"--message=", []string{"--message=feat: z"}, "feat: z", true},
		{"-F file", []string{"-q", "-F", msgFile}, "fix: from a file", true},
		{"--file=", []string{"--file=" + msgFile}, "fix: from a file", true},
		{"stdin is unknown", []string{"-F", "-"}, "", false},
		{"no message", []string{"--no-verify"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := commitHeader(tt.args)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("commitHeader(%q) = %q, %v; want %q, %v", tt.args, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestParseEnvLines(t *testing.T) {
	got, err := parseEnvLines("# pinned node\nPATH=/node/bin:/usr/bin\n\nTMPDIR=/repo/.git/tmp\n")
	if err != nil {
		t.Fatalf("parseEnvLines = %v", err)
	}
	want := []string{"PATH=/node/bin:/usr/bin", "TMPDIR=/repo/.git/tmp"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("parseEnvLines = %q, want %q", got, want)
	}
	for _, bad := range []string{"just words", "=value", "BAD KEY=1"} {
		if _, err := parseEnvLines(bad); err == nil {
			t.Errorf("parseEnvLines(%q) accepted", bad)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"issue-12", "t1088.merge_2"} {
		if err := validName(ok); err != nil {
			t.Errorf("validName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".hidden", "a/b", "a b"} {
		if validName(bad) == nil {
			t.Errorf("validName(%q) accepted", bad)
		}
	}
}

func TestWaitForNewExitsIgnoresOldOnes(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.exit")
	writeFile(t, old, "0\n")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	since := time.Now().Add(-time.Minute)
	done := make(chan []finishedRun, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		runs, err := waitForNewExits(ctx, filepath.Join(dir, "*.exit"), since, time.Millisecond)
		if err != nil {
			t.Errorf("waitForNewExits = %v", err)
		}
		done <- runs
	}()
	if err := writeExit(filepath.Join(dir, "new.exit"), 1); err != nil {
		t.Fatalf("writeExit: %v", err)
	}
	runs := <-done
	if len(runs) != 1 || runs[0].Name != "new" || runs[0].Code != "1" {
		t.Errorf("runs = %+v, want only new: 1", runs)
	}
}

func TestStatusTextListsHoldersThenTheLineInOrder(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	snap := commitqueue.Snapshot{
		Slots: 2,
		Held:  []commitqueue.Slot{{Number: 1, Name: "issue-1", Repo: "/code/app/.git", StartedAt: now.Add(-90 * time.Second).UnixNano()}},
		Waiting: []commitqueue.Ticket{
			{Name: "issue-2", Repo: "/code/other/.git", QueuedAt: now.Add(-60 * time.Second).UnixNano()},
			{Name: "issue-3", Repo: "/code/app/.git", QueuedAt: now.Add(-5 * time.Second).UnixNano()},
		},
	}
	var out bytes.Buffer
	writeStatusText(&out, "/state", snap, now)
	text := out.String()
	for _, want := range []string{"1 of 2 slots held · 2 waiting", "slot-1  issue-1", "app", "1m30s", "#1     issue-2", "other", "#2     issue-3"} {
		if !strings.Contains(text, want) {
			t.Errorf("status lacks %q:\n%s", want, text)
		}
	}
}

func TestDispatchRejectsBadUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	for _, args := range [][]string{{}, {"nope"}, {"run", "only-name"}, {"release"}} {
		if code := dispatch(args, &out, &errOut); code != 2 {
			t.Errorf("dispatch(%q) = %d, want 2", args, code)
		}
	}
}
