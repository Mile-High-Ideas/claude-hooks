package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeLinterScripts are stand-ins for oxlint and eslint. Each flags every line
// containing BAD; with --fix it first rewrites BAD to GOOD in the files or
// directories named on the command line, like the real --fix. oxlint prints compact findings, eslint the stylish
// format with absolute paths, so both parsers are exercised.
var fakeLinterScripts = map[string]string{
	"oxlint": `#!/bin/sh
fix=0
for a in "$@"; do [ "$a" = --fix ] && fix=1; done
for a in "$@"; do
  [ "$a" = --fix ] && continue
  [ $fix = 1 ] && grep -rl --exclude-dir=node_modules BAD "$a" /dev/null | while read f; do
    sed -i.orig 's/BAD/GOOD/' "$f" && rm -f "$f.orig"
  done
done
for a in "$@"; do
  [ "$a" = --fix ] && continue
  grep -rn --exclude-dir=node_modules BAD "$a" /dev/null | while IFS=: read f n rest; do
    echo "$f:$n:1: error fake(no-bad): BAD found"
  done
done
`,
	"eslint": `#!/bin/sh
fix=0
for a in "$@"; do [ "$a" = --fix ] && fix=1; done
for a in "$@"; do
  [ "$a" = --fix ] && continue
  [ $fix = 1 ] && grep -rl --exclude-dir=node_modules BAD "$a" /dev/null | while read f; do
    sed -i.orig 's/BAD/GOOD/' "$f" && rm -f "$f.orig"
  done
done
for a in "$@"; do
  [ "$a" = --fix ] && continue
  grep -rln --exclude-dir=node_modules BAD "$a" /dev/null | while read f; do
    echo "$PWD/${f#./}"
    echo "  1:1  error  BAD found  fake/no-bad"
  done
done
`,
}

// lintFixRepo is a git repo holding one app at apps/web whose node_modules/.bin
// has a fake linter. It is the working directory for the rest of the test.
type lintFixRepo struct {
	t   *testing.T
	dir string
}

func newLintFixRepo(t *testing.T, linter string) *lintFixRepo {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake linter is a POSIX shell script")
	}
	clearGitHookEnv(t)
	r := &lintFixRepo{t: t, dir: t.TempDir()}

	bin := filepath.Join(r.dir, "apps", "web", "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, linter), []byte(fakeLinterScripts[linter]), 0o755); err != nil {
		t.Fatal(err)
	}
	r.write(".gitignore", "node_modules/\n")
	r.write("apps/web/clean.ts", "export const ok = 1\n")
	r.write("apps/web/committed.ts", "export const a = 1\n")
	r.git("init", "-q", "-b", "main")
	r.git("add", ".")
	r.git("commit", "-q", "-m", "initial")

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(r.dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	return r
}

func (r *lintFixRepo) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *lintFixRepo) read(name string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func (r *lintFixRepo) git(args ...string) {
	r.t.Helper()
	if err := runGitCommand(r.dir, args...); err != nil {
		r.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
}

// indexed returns the staged content of name.
func (r *lintFixRepo) indexed(name string) string {
	r.t.Helper()
	cmd := exec.Command("git", "show", ":"+name)
	cmd.Dir = r.dir
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git show :%s: %v", name, err)
	}
	return string(out)
}

// unstaged reports whether name differs between the working tree and index.
func (r *lintFixRepo) unstaged(name string) bool {
	r.t.Helper()
	cmd := exec.Command("git", "diff", "--quiet", "--", name)
	cmd.Dir = r.dir
	return cmd.Run() != nil
}

// lint runs the full lint for apps/web the way runLintTo does, with the
// repo's staged files.
func (r *lintFixRepo) lint(linter string) (string, error) {
	r.t.Helper()
	staged, err := filesToCheck()
	if err != nil {
		r.t.Fatal(err)
	}
	return runFilteredLintBuffered("web", "apps/web", staged, LintFilter{Linter: linter})
}

var lintFixLinters = []string{"oxlint", "eslint"}

func TestFullLintFixesAndRestagesStagedFile(t *testing.T) {
	for _, linter := range lintFixLinters {
		t.Run(linter, func(t *testing.T) {
			r := newLintFixRepo(t, linter)
			r.write("apps/web/new.ts", "import BAD from './x'\n")
			r.git("add", "apps/web/new.ts")

			out, err := r.lint(linter)
			if err != nil {
				t.Fatalf("lint failed: %v\n%s", err, out)
			}
			want := "import GOOD from './x'\n"
			if got := r.indexed("apps/web/new.ts"); got != want {
				t.Errorf("staged copy = %q, want the fix %q", got, want)
			}
			if r.unstaged("apps/web/new.ts") {
				t.Error("fixed file left with unstaged changes")
			}
			if !strings.Contains(out, "autofixed and re-staged apps/web/new.ts") {
				t.Errorf("output does not name the re-staged file:\n%s", out)
			}
		})
	}
}

func TestFullLintFailsOnUnstagedFileWithoutTouchingIt(t *testing.T) {
	for _, linter := range lintFixLinters {
		t.Run(linter, func(t *testing.T) {
			r := newLintFixRepo(t, linter)
			bad := "export const BAD = 1\n"
			r.write("apps/web/committed.ts", bad)
			r.write("apps/web/staged.ts", "export const fine = 1\n")
			r.git("add", "apps/web/staged.ts")

			out, err := r.lint(linter)
			if err == nil {
				t.Fatalf("lint passed with an autofixable error in an unstaged file:\n%s", out)
			}
			if !strings.Contains(out, "committed.ts") {
				t.Errorf("failure does not name the unstaged file:\n%s", out)
			}
			if got := r.read("apps/web/committed.ts"); got != bad {
				t.Errorf("unstaged file rewritten to %q", got)
			}
			if got := r.indexed("apps/web/committed.ts"); got != "export const a = 1\n" {
				t.Errorf("unstaged file was staged: index has %q", got)
			}
		})
	}
}

func TestFullLintLeavesPartiallyStagedFileAlone(t *testing.T) {
	for _, linter := range lintFixLinters {
		t.Run(linter, func(t *testing.T) {
			r := newLintFixRepo(t, linter)
			staged := "export const BAD = 1\n"
			r.write("apps/web/committed.ts", staged)
			r.git("add", "apps/web/committed.ts")
			worktree := staged + "export const wip = 2\n"
			r.write("apps/web/committed.ts", worktree)

			out, err := r.lint(linter)
			if err == nil {
				t.Fatalf("lint passed with an error in a partially staged file:\n%s", out)
			}
			if got := r.indexed("apps/web/committed.ts"); got != staged {
				t.Errorf("index = %q, want the staged copy %q untouched", got, staged)
			}
			if got := r.read("apps/web/committed.ts"); got != worktree {
				t.Errorf("working copy rewritten to %q", got)
			}
			if !strings.Contains(out, "not autofixed, has unstaged changes: apps/web/committed.ts") {
				t.Errorf("output does not name the partially staged file:\n%s", out)
			}
		})
	}
}

func TestFullLintAllFilesReportsWithoutFixing(t *testing.T) {
	for _, linter := range lintFixLinters {
		t.Run(linter, func(t *testing.T) {
			r := newLintFixRepo(t, linter)
			setAllFiles(t, true)

			if out, err := r.lint(linter); err != nil {
				t.Fatalf("clean repo failed --all-files lint: %v\n%s", err, out)
			}

			bad := "export const BAD = 1\n"
			r.write("apps/web/committed.ts", bad)
			r.git("add", "apps/web/committed.ts")
			out, err := r.lint(linter)
			if err == nil {
				t.Fatalf("--all-files lint passed with an error:\n%s", out)
			}
			if !strings.Contains(out, "committed.ts") {
				t.Errorf("failure does not name the file:\n%s", out)
			}
			if got := r.read("apps/web/committed.ts"); got != bad {
				t.Errorf("--all-files rewrote the file to %q", got)
			}
		})
	}
}
