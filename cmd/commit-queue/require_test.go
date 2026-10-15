package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
)

// TestMain lets a git hook in these tests run this test binary as the
// commit-queue command, so the hook exercises the real gate.
func TestMain(m *testing.M) {
	if os.Getenv("COMMIT_QUEUE_TEST_AS_COMMAND") == "1" {
		os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// chdir moves the test into dir and back afterwards.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func agentShell(t *testing.T, on bool) {
	t.Helper()
	t.Setenv("AI_AGENT", "")
	t.Setenv("CLAUDECODE", "")
	if on {
		t.Setenv("CLAUDECODE", "1")
	}
}

func TestRequireLetsAPersonCommit(t *testing.T) {
	isolate(t)
	agentShell(t, false)
	if code := requireQueued(&bytes.Buffer{}); code != 0 {
		t.Errorf("requireQueued = %d outside an agent shell, want 0", code)
	}
}

func TestRequireRefusesAnAgentsDirectCommit(t *testing.T) {
	state := isolate(t)
	repo := newRepo(t)
	chdir(t, repo)
	agentShell(t, true)

	var errOut bytes.Buffer
	if code := requireQueued(&errOut); code != 1 || !strings.Contains(errOut.String(), "BLOCKED") {
		t.Errorf("requireQueued = %d, %q; want 1 and the BLOCKED message", code, errOut.String())
	}

	// The marker alone, for a name holding no slot, is not enough.
	t.Setenv(runMarkerEnv, "issue-1")
	if code := requireQueued(&bytes.Buffer{}); code != 1 {
		t.Errorf("requireQueued with a bare marker = %d, want 1", code)
	}

	// With a live holder of that name in this repo, it passes.
	info, err := resolveRepo(repo)
	if err != nil {
		t.Fatalf("resolveRepo: %v", err)
	}
	q, err := commitqueue.New(commitqueue.Options{Dir: state})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := q.Acquire(ctx, commitqueue.Caller{Name: "issue-1", Repo: info.CommonDir}, nil, nil); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if code := requireQueued(&bytes.Buffer{}); code != 0 {
		t.Errorf("requireQueued with a held slot = %d, want 0", code)
	}
	if code := dispatch([]string{"holds", "issue-1"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Errorf("holds issue-1 = %d, want 0", code)
	}
	if code := dispatch([]string{"holds", "issue-2"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Errorf("holds issue-2 = %d, want 1", code)
	}
}

// A hook that runs `commit-queue require` lets a queued run's commit
// through and refuses the same agent's direct commit.
func TestRequireInAHookAcceptsOnlyQueuedCommits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook")
	}
	isolate(t)
	commitIdentity(t)
	repo := newRepo(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	hook := "#!/bin/sh\nCOMMIT_QUEUE_TEST_AS_COMMAND=1 exec '" + self + "' require\n"
	writeFile(t, filepath.Join(repo, ".git", "hooks", "pre-commit"), hook)
	agentShell(t, true)

	writeFile(t, filepath.Join(repo, "queued.txt"), "q\n")
	git(t, repo, "add", "queued.txt")
	if code := runCommit("queued", repo, []string{"-m", "feat: queued"}, 0); code != 0 {
		t.Fatalf("runCommit = %d, want the queued commit allowed", code)
	}

	writeFile(t, filepath.Join(repo, "direct.txt"), "d\n")
	git(t, repo, "add", "direct.txt")
	head := git(t, repo, "rev-parse", "HEAD")
	if err := gitTry(repo, "commit", "-q", "-m", "feat: direct"); err == nil {
		t.Error("a direct commit from an agent shell was allowed")
	}
	if got := git(t, repo, "rev-parse", "HEAD"); got != head {
		t.Error("HEAD moved despite the refused commit")
	}
}
