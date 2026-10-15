package main

import (
	"io"
	"os"
)

// runMarkerEnv is set on the `git commit` that `run` makes, to the run's
// name. On its own it proves nothing, since anyone can set it; `require`
// accepts it only when that name holds a slot through a live runner.
const runMarkerEnv = "COMMIT_QUEUE_RUN"

// blockedMessage is what `require` prints when it refuses a commit.
const blockedMessage = `BLOCKED: agents commit through the commit queue, not with a direct commit.
That includes git commit --amend, and a merge commit that git merge makes itself.

  nohup commit-queue run <name> <absolute-worktree-path> -F <msg-file> >/dev/null 2>&1 &
  commit-queue wait

For a merge, run git merge --no-commit <ref>, resolve and stage any conflicts,
then commit through commit-queue run as above.

The queue runs at most N pre-commit suites on this machine at once, first come,
first served; commit-queue status shows the line. A person committing from a
Claude Code "!" prompt should commit from a regular terminal instead.
`

// isAgentShell reports whether this process runs in an AI agent's shell:
// Claude Code sets CLAUDECODE=1, and other agents set AI_AGENT. A person's
// own terminal has neither.
func isAgentShell() bool {
	return os.Getenv("CLAUDECODE") == "1" || os.Getenv("AI_AGENT") != ""
}

// requireQueued is a commit hook gate: it allows a commit unless it comes
// from an agent's shell without a queued run holding a slot for it. Wire it
// first in pre-commit, and in pre-merge-commit, since a merge that git merge
// commits itself skips pre-commit.
func requireQueued(errOut io.Writer) int {
	if !isAgentShell() {
		return 0
	}
	if name := os.Getenv(runMarkerEnv); name != "" && holdsSlot(name) {
		return 0
	}
	say(errOut, "%s", blockedMessage)
	return 1
}

// holdsSlot reports whether name holds a slot for this repo through a live
// runner. Any error reading the queue counts as no.
func holdsSlot(name string) bool {
	q, _, repo, err := openQueue(".", 0)
	if err != nil {
		return false
	}
	held, err := q.Holds(name, repo.CommonDir)
	return err == nil && held
}
