package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runResult reads what a run left behind.
func runResult(t *testing.T, stateDir, repo, name string) (log, exit string) {
	t.Helper()
	info, err := resolveRepo(repo)
	if err != nil {
		t.Fatalf("resolveRepo: %v", err)
	}
	dir := filepath.Join(stateDir, "runs", info.Key)
	return readFile(t, filepath.Join(dir, name+".log")), strings.TrimSpace(readFile(t, filepath.Join(dir, name+".exit")))
}

func TestRunCommitsAndRecordsTheRun(t *testing.T) {
	state := isolate(t)
	commitIdentity(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	git(t, repo, "add", "a.txt")

	if code := runCommit("issue-1", repo, []string{"-m", "feat: add a"}, 0); code != 0 {
		t.Fatalf("runCommit = %d, want 0", code)
	}
	if got := git(t, repo, "log", "-1", "--format=%s"); got != "feat: add a" {
		t.Errorf("last commit = %q, want the queued one", got)
	}
	log, exit := runResult(t, state, repo, "issue-1")
	if exit != "0" {
		t.Errorf("exit file = %q, want 0", exit)
	}
	for _, want := range []string{
		"[run-commit] queued at ",
		" on main in ",
		"[run-commit] subject: feat: add a",
		"[commit-slot] issue-1 holds slot-1 from ",
		"[commit-slot] issue-1 released slot-1",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestRunReportsAFailingHookAndFreesTheSlot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook")
	}
	state := isolate(t)
	commitIdentity(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".git", "hooks", "pre-commit"), "#!/bin/sh\necho 'lint failed' >&2\nexit 3\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	git(t, repo, "add", "b.txt")

	if code := runCommit("issue-2", repo, []string{"-m", "feat: add b"}, 0); code == 0 {
		t.Fatal("runCommit = 0, want the hook's failure")
	}
	log, exit := runResult(t, state, repo, "issue-2")
	if exit == "0" {
		t.Errorf("exit file = %q, want non-zero", exit)
	}
	if !strings.Contains(log, "lint failed") || !strings.Contains(log, "released slot-1") {
		t.Errorf("log should hold the hook output and the release:\n%s", log)
	}
	// The slot is free for the next run.
	writeFile(t, filepath.Join(repo, ".git", "hooks", "pre-commit"), "#!/bin/sh\nexit 0\n")
	if code := runCommit("issue-3", repo, []string{"-m", "feat: add b"}, 0); code != 0 {
		t.Errorf("next runCommit = %d, want 0", code)
	}
}

func TestRunRejectsALongHeaderBeforeQueueing(t *testing.T) {
	state := isolate(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".pre-commit.json"), `{
  // comments are allowed
  "commitQueue": { "maxHeaderLength": 20 }
}`)
	head := git(t, repo, "rev-parse", "HEAD")

	if code := runCommit("long", repo, []string{"-m", "feat: a header well over twenty characters"}, 0); code != 1 {
		t.Fatalf("runCommit = %d, want 1", code)
	}
	if got := git(t, repo, "rev-parse", "HEAD"); got != head {
		t.Error("a commit was made despite the long header")
	}
	log, _ := runResult(t, state, repo, "long")
	if !strings.Contains(log, "commitlint allows 20") || strings.Contains(log, "holds slot") {
		t.Errorf("log should reject before taking a slot:\n%s", log)
	}
}

func TestRunPassesTheSetupScriptEnvironmentToHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	isolate(t)
	commitIdentity(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "scripts", "setup.sh"), "#!/bin/sh\necho 'note for the log' >&2\necho '# a comment'\necho 'QUEUE_TEST_VALUE=from-setup'\n")
	writeFile(t, filepath.Join(repo, ".pre-commit.json"), `{"commitQueue": {"setupScript": "scripts/setup.sh"}}`)
	seen := filepath.Join(t.TempDir(), "seen")
	writeFile(t, filepath.Join(repo, ".git", "hooks", "pre-commit"), "#!/bin/sh\nprintf '%s' \"$QUEUE_TEST_VALUE\" > '"+seen+"'\n")
	writeFile(t, filepath.Join(repo, "c.txt"), "c\n")
	git(t, repo, "add", "c.txt")

	if code := runCommit("env", repo, []string{"-m", "feat: add c"}, 0); code != 0 {
		t.Fatalf("runCommit = %d, want 0", code)
	}
	if got := readFile(t, seen); got != "from-setup" {
		t.Errorf("hook saw QUEUE_TEST_VALUE=%q, want from-setup", got)
	}
}

func TestRunRejectsAnUnsafeName(t *testing.T) {
	isolate(t)
	if code := runCommit("../escape", t.TempDir(), nil, 0); code != 2 {
		t.Errorf("runCommit(../escape) = %d, want 2", code)
	}
}

func TestStateDirFollowsScope(t *testing.T) {
	isolate(t)
	t.Setenv("COMMIT_QUEUE_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/state")
	repo := repoInfo{CommonDir: "/code/app/.git"}

	machine, err := stateDir(queueConfig{}, repo)
	if err != nil || machine != filepath.Join("/state", "claude-hooks", "commit-queue") {
		t.Errorf("machine scope dir = %q, %v", machine, err)
	}
	repoScoped, err := stateDir(queueConfig{Scope: "repo"}, repo)
	if err != nil || repoScoped != filepath.Join("/code/app/.git", "commit-queue") {
		t.Errorf("repo scope dir = %q, %v", repoScoped, err)
	}
	t.Setenv("COMMIT_QUEUE_DIR", "/override")
	if got, _ := stateDir(queueConfig{Scope: "repo"}, repo); got != "/override" {
		t.Errorf("COMMIT_QUEUE_DIR ignored: %q", got)
	}
}

func TestSlotCountPrecedence(t *testing.T) {
	isolate(t)
	tests := []struct {
		name string
		flag int
		env  string
		cfg  queueConfig
		want int
	}{
		{"default", 0, "", queueConfig{}, 2},
		{"repo config", 0, "", queueConfig{Scope: "repo", Slots: 3}, 3},
		{"machine scope ignores repo config", 0, "", queueConfig{Slots: 3}, 2},
		{"env beats config", 0, "4", queueConfig{Scope: "repo", Slots: 3}, 4},
		{"flag beats env", 5, "4", queueConfig{}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COMMIT_QUEUE_SLOTS", tt.env)
			got, err := slotCount(tt.flag, tt.cfg)
			if err != nil || got != tt.want {
				t.Errorf("slotCount = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
	t.Setenv("COMMIT_QUEUE_SLOTS", "zero")
	if _, err := slotCount(0, queueConfig{}); err == nil {
		t.Error("slotCount accepted COMMIT_QUEUE_SLOTS=zero")
	}
}

func TestLoadQueueConfigRejectsAnUnknownScope(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".pre-commit.json"), `{"commitQueue": {"scope": "global"}}`)
	if _, err := loadQueueConfig(dir); err == nil {
		t.Error("scope \"global\" accepted")
	}
}
