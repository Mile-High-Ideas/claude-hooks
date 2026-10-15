package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/milehighideas/claude-hooks/internal/commitqueue"
)

// The run log keeps the line formats of the shell queue it replaces
// (scripts/agent-commit/run-commit.sh in projectparabellum.com), so one
// parser reads old and new logs:
//
//	[run-commit] queued at <unix> pid <pid> on <branch> in <worktree>
//	[run-commit] subject: <first line of the message>
//	[commit-slot] <name> holds slot-<n> from HH:MM:SS
//	[commit-slot] <name> released slot-<n>

// runCommit queues a commit, runs `git commit <args>` in worktree once it
// holds a slot, and records the outcome as runs/<repo>/<name>.log and then
// <name>.exit. The exit file is written last, through a rename, so its
// presence means the log is complete. It returns git's exit code.
func runCommit(name, worktree string, gitArgs []string, slotsFlag int) int {
	if err := validName(name); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	abs, err := filepath.Abs(worktree)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	q, cfg, repo, err := openQueue(abs, slotsFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dir := runsDir(q, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	logPath := filepath.Join(dir, name+".log")
	exitPath := filepath.Join(dir, name+".exit")
	_ = os.Remove(exitPath)
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = logFile.Close() }()

	code := queueAndCommit(q, cfg, repo, name, abs, gitArgs, logFile)
	if err := writeExit(exitPath, code); err != nil {
		say(logFile, "[run-commit] could not write exit code: %v\n", err)
	}
	return code
}

// queueAndCommit does the work between opening the log and recording the
// exit code.
func queueAndCommit(q *commitqueue.Queue, cfg queueConfig, repo repoInfo, name, worktree string, gitArgs []string, log io.Writer) int {
	branch, _ := gitOutput(worktree, "branch", "--show-current")
	if branch == "" {
		branch = "detached"
	}
	say(log, "[run-commit] queued at %d pid %d on %s in %s\n", time.Now().Unix(), os.Getpid(), branch, worktree)

	if header, ok := commitHeader(gitArgs); ok {
		say(log, "[run-commit] subject: %s\n", header)
		if cfg.MaxHeaderLength > 0 && len(header) > cfg.MaxHeaderLength {
			say(log, "[run-commit] commit header is %d characters; commitlint allows %d:\n[run-commit]   %s\n", len(header), cfg.MaxHeaderLength, header)
			return 1
		}
	}

	env, err := setupEnv(worktree, cfg.SetupScript, log)
	if err != nil {
		say(log, "[run-commit] %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	caller := commitqueue.Caller{Name: name, Repo: repo.CommonDir}
	slot, err := q.Acquire(ctx, caller,
		func(position int) {
			say(log, "[commit-queue] %s waiting, position %d of the line\n", name, position)
		},
		func(r commitqueue.Reclaimed) { logReclaim(log, r) },
	)
	if err != nil {
		say(log, "[commit-queue] %s gave up waiting: %v\n", name, err)
		return signalExitCode(ctx, 1)
	}
	say(log, "[commit-slot] %s holds slot-%d from %s\n", name, slot.Number, time.Unix(0, slot.StartedAt).Format("15:04:05"))
	defer func() {
		if _, err := q.Release(name, repo.CommonDir); err != nil {
			say(log, "[commit-slot] %s could not release slot-%d: %v\n", name, slot.Number, err)
			return
		}
		say(log, "[commit-slot] %s released slot-%d\n", name, slot.Number)
	}()

	// Mark the commit as this run's, for `commit-queue require` in its hooks.
	env = append(env, runMarkerEnv+"="+name)
	return gitCommit(ctx, worktree, gitArgs, env, log)
}

// gitCommit runs git commit with the extra environment, forwarding an
// interrupt to it rather than abandoning it mid-hook.
func gitCommit(ctx context.Context, worktree string, args, env []string, log io.Writer) int {
	cmd := exec.Command("git", append([]string{"commit"}, args...)...)
	cmd.Dir = worktree
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		say(log, "[run-commit] start git commit: %v\n", err)
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return exitCode(err)
	case <-ctx.Done():
		// Windows cannot deliver an interrupt to another process.
		if cmd.Process.Signal(os.Interrupt) != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return signalExitCode(ctx, 130)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	return 1
}

// signalExitCode is 130 when the run was interrupted, otherwise fallback.
func signalExitCode(ctx context.Context, fallback int) int {
	if ctx.Err() != nil {
		return 130
	}
	return fallback
}

func logReclaim(log io.Writer, r commitqueue.Reclaimed) {
	if r.Kind == "slot" {
		say(log, "[commit-slot] reclaimed stale slot-%d from %s (%s)\n", r.Slot, r.Name, r.Reason)
		return
	}
	say(log, "[commit-queue] dropped the ticket of %s (%s)\n", r.Name, r.Reason)
}

// writeExit writes the exit code through a temporary name, so a watcher
// never reads a half-written file.
func writeExit(path string, code int) error {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d\n", code)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
