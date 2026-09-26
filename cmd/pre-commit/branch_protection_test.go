package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepoEnvVars point git at a specific repository. git exports them to its
// hooks, so when `go test` runs inside a pre-commit hook every git command a
// test spawns would act on the repository being committed to, not the temp
// repo in cmd.Dir.
var gitRepoEnvVars = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_PREFIX",
}

// isolateFromOuterGitRepo unsets gitRepoEnvVars for the rest of the test and
// restores them afterwards. Without it, running the suite from a hook in a
// linked worktree committed this fixture's "Initial commit" onto the branch
// being committed.
func isolateFromOuterGitRepo(t *testing.T) {
	t.Helper()
	for _, name := range gitRepoEnvVars {
		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		_ = os.Unsetenv(name)
		t.Cleanup(func() { _ = os.Setenv(name, value) })
	}
}

func TestIsolateFromOuterGitRepo(t *testing.T) {
	const outerDir = "/outer/repo/.git"
	const outerIndex = "/outer/repo/.git/index"
	t.Setenv("GIT_DIR", outerDir)
	t.Setenv("GIT_INDEX_FILE", outerIndex)

	t.Run("unsets hook variables", func(t *testing.T) {
		isolateFromOuterGitRepo(t)
		for _, name := range []string{"GIT_DIR", "GIT_INDEX_FILE"} {
			if value, ok := os.LookupEnv(name); ok {
				t.Errorf("%s still set to %q", name, value)
			}
		}
	})

	if got := os.Getenv("GIT_DIR"); got != outerDir {
		t.Errorf("GIT_DIR not restored after the test: got %q, want %q", got, outerDir)
	}
	if got := os.Getenv("GIT_INDEX_FILE"); got != outerIndex {
		t.Errorf("GIT_INDEX_FILE not restored after the test: got %q, want %q", got, outerIndex)
	}
}

func TestCheckBranchProtection(t *testing.T) {
	isolateFromOuterGitRepo(t)

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
