package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolate unsets every GIT_* variable and points the queue at a temporary
// directory for the rest of the test. A git hook runs with GIT_DIR and
// friends set to the repo being committed, and git honours them over
// cmd.Dir, so without this `go test` from a pre-commit hook would commit
// into that repo instead of the test's.
func isolate(t *testing.T) string {
	t.Helper()
	for _, kv := range os.Environ() {
		key, value, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(key, "GIT_") && !strings.HasPrefix(key, "COMMIT_QUEUE_") {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() { _ = os.Setenv(key, value) })
	}
	dir := t.TempDir()
	t.Setenv("COMMIT_QUEUE_DIR", dir)
	return dir
}

// newRepo makes a git repository with one commit and returns its path.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// commitIdentity lets runCommit's own git commit succeed in a bare test
// environment, where no user.name is configured.
func commitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

// gitTry is like git but returns the error, for commands expected to fail.
func gitTry(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}
