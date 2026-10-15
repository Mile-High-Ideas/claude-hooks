package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckBranchProtection(t *testing.T) {
	clearGitHookEnv(t)

	// Create a temporary git repository for testing
	tempDir, err := os.MkdirTemp("", "branch-protection-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Initialize git repo
	if err := runGitCommand(tempDir, "init"); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Create initial commit to establish branch
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := runGitCommand(tempDir, "add", "."); err != nil {
		t.Fatalf("Failed to stage files: %v", err)
	}
	if err := runGitCommand(tempDir, "commit", "-m", "Initial commit"); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}

	// Get current branch name
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = tempDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get current branch: %v", err)
	}
	currentBranch := strings.TrimSpace(string(output))

	// Save original directory and change to temp dir
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current dir: %v", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change to temp dir: %v", err)
	}

	tests := []struct {
		name              string
		protectedBranches []string
		skipEnvVar        string
		wantErr           bool
		errContains       string
	}{
		{
			name:              "protected branch detected",
			protectedBranches: []string{currentBranch},
			skipEnvVar:        "",
			wantErr:           true,
			errContains:       "direct commits to the " + currentBranch + " branch are not allowed",
		},
		{
			name:              "non-protected branch allowed",
			protectedBranches: []string{"some-other-branch", "another-protected"},
			skipEnvVar:        "",
			wantErr:           false,
		},
		{
			name:              "SKIP_BRANCH_PROTECTION bypasses check",
			protectedBranches: []string{currentBranch},
			skipEnvVar:        "1",
			wantErr:           false,
		},
		{
			name:              "empty protected branches list",
			protectedBranches: []string{},
			skipEnvVar:        "",
			wantErr:           false,
		},
		{
			name:              "multiple protected branches with match",
			protectedBranches: []string{"main", "master", currentBranch, "develop"},
			skipEnvVar:        "",
			wantErr:           true,
			errContains:       "direct commits to the " + currentBranch + " branch are not allowed",
		},
		{
			name:              "multiple protected branches without match",
			protectedBranches: []string{"release", "production", "develop", "staging"},
			skipEnvVar:        "",
			wantErr:           false,
		},
		{
			name:              "SKIP_BRANCH_PROTECTION with any value bypasses",
			protectedBranches: []string{currentBranch},
			skipEnvVar:        "true",
			wantErr:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set or unset the env var
			if tt.skipEnvVar != "" {
				_ = os.Setenv("SKIP_BRANCH_PROTECTION", tt.skipEnvVar)
				defer func() { _ = os.Unsetenv("SKIP_BRANCH_PROTECTION") }()
			} else {
				_ = os.Unsetenv("SKIP_BRANCH_PROTECTION")
			}

			err := checkBranchProtection(tt.protectedBranches)

			if tt.wantErr {
				if err == nil {
					t.Errorf("checkBranchProtection() expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("checkBranchProtection() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("checkBranchProtection() unexpected error: %v", err)
				}
			}
		})
	}
}

// runGitCommand runs a git command in the specified directory
func runGitCommand(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	return cmd.Run()
}

// clearGitHookEnv unsets every GIT_* variable for the rest of the test and
// restores them afterwards. A git hook runs with GIT_DIR, GIT_INDEX_FILE and
// friends pointing at the repo being committed, and git honours them over
// cmd.Dir. Without this, `go test` run from this repo's own pre-commit hook
// made the temp-dir commit in TestCheckBranchProtection land on the branch
// being committed: an "Initial commit" that deleted every file.
func clearGitHookEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		key, value, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(key, "GIT_") {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() { _ = os.Setenv(key, value) })
	}
}

func TestClearGitHookEnvKeepsTempRepoOffTheHookRepo(t *testing.T) {
	// The repo a hook would be committing to.
	hookRepo := t.TempDir()
	if err := runGitCommand(hookRepo, "init"); err != nil {
		t.Fatalf("init hook repo: %v", err)
	}
	t.Setenv("GIT_DIR", filepath.Join(hookRepo, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(hookRepo, ".git", "index"))

	clearGitHookEnv(t)

	scratch := t.TempDir()
	if err := os.WriteFile(filepath.Join(scratch, "test.txt"), []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"commit", "-m", "Initial commit"}} {
		if err := runGitCommand(scratch, args...); err != nil {
			t.Fatalf("git %v in scratch repo: %v", args, err)
		}
	}

	// The commit belongs to the scratch repo; the hook repo has none.
	if err := runGitCommand(scratch, "rev-parse", "--verify", "HEAD"); err != nil {
		t.Errorf("scratch repo has no commit: %v", err)
	}
	if err := runGitCommand(hookRepo, "rev-parse", "--verify", "HEAD"); err == nil {
		t.Error("hook repo gained a commit: the scratch repo's git commands wrote to GIT_DIR")
	}
}
