package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newAllFilesRepo creates a repo on branch main with a.ts and b.ts committed,
// b.ts then deleted from the working tree, c.ts untracked and d.ts staged as a
// new file, and makes it the working directory for the rest of the test.
func newAllFilesRepo(t *testing.T) string {
	t.Helper()
	clearGitHookEnv(t)

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if err := runGitCommand(dir, args...); err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
	}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("export {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main")
	write("a.ts")
	write("b.ts")
	git("add", "a.ts", "b.ts")
	git("commit", "-q", "-m", "initial")
	if err := os.Remove(filepath.Join(dir, "b.ts")); err != nil {
		t.Fatal(err)
	}
	write("c.ts")
	write("d.ts")
	git("add", "d.ts")

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	return dir
}

func setAllFiles(t *testing.T, value bool) {
	t.Helper()
	previous := allFiles
	allFiles = value
	t.Cleanup(func() { allFiles = previous })
}

func TestGetTrackedFilesSkipsDeletedAndUntracked(t *testing.T) {
	newAllFilesRepo(t)

	got, err := getTrackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	// b.ts is in the index but gone from disk; c.ts was never added.
	want := []string{"a.ts", "d.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getTrackedFiles() = %v, want %v", got, want)
	}
}

func TestFilesToCheck(t *testing.T) {
	tests := []struct {
		name     string
		allFiles bool
		want     []string
	}{
		{name: "staged files by default", allFiles: false, want: []string{"d.ts"}},
		{name: "every tracked file with --all-files", allFiles: true, want: []string{"a.ts", "d.ts"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newAllFilesRepo(t)
			setAllFiles(t, tt.allFiles)

			got, err := filesToCheck()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filesToCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetNewlyAddedFilesIsEmptyWithAllFiles(t *testing.T) {
	newAllFilesRepo(t)

	added, err := getNewlyAddedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !added["d.ts"] {
		t.Fatalf("staged mode: getNewlyAddedFiles() = %v, want d.ts", added)
	}

	setAllFiles(t, true)
	added, err = getNewlyAddedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("--all-files: getNewlyAddedFiles() = %v, want empty", added)
	}
}

// Branch protection and the changelog gate are about a commit. On main with a
// staged file and no changelog fragment, a normal run fails on each of them;
// an --all-files audit makes no commit and must not.
func TestRunAllFilesSkipsCommitOnlyGates(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name:    "branch protection",
			config:  `{"features": {"branchProtection": true}, "protectedBranches": ["main"]}`,
			wantErr: "direct commits to the main branch",
		},
		{
			name:    "changelog",
			config:  `{"features": {"changelog": true}}`,
			wantErr: "changelog fragment required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newAllFilesRepo(t)
			if err := os.WriteFile(filepath.Join(dir, ".pre-commit.json"), []byte(tt.config), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SKIP_BRANCH_PROTECTION", "")
			t.Setenv("SKIP_CHANGELOG_CHECK", "")

			setAllFiles(t, false)
			if err := run(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("staged run: err = %v, want %q", err, tt.wantErr)
			}

			setAllFiles(t, true)
			if err := run(); err != nil {
				t.Errorf("--all-files run: err = %v, want nil", err)
			}
		})
	}
}
