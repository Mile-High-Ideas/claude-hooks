# commit-queue

Runs `git commit` through a queue so that at most N pre-commit suites run on
a machine at once, and serves waiting commits strictly in the order they
arrived.

It exists for machines where several agents commit in parallel worktrees. A
full pre-commit suite (lint, typecheck, every app's tests) can occupy most of
a large machine's CPU; running five at once makes all five slow and flaky.

## Usage

```bash
# Queue a commit. Start it detached so it outlives the agent that queued it.
nohup commit-queue run issue-12 "$PWD" -F /tmp/msg.txt >/dev/null 2>&1 &

# Block until a run in this repo finishes; prints "<name>: <exit code>".
commit-queue wait

# Who holds a slot, and who is waiting, in the order they will be served.
commit-queue status
commit-queue status --json

# Hold a slot for work that is not one git commit, then free it.
commit-queue acquire verify-12          # owner: the calling shell
commit-queue acquire verify-12 --pid $$ # or any long-lived process
commit-queue release verify-12
```

`run <name> <worktree> <git commit args...>` writes
`<state>/runs/<repo>/<name>.log` (everything) and then `<name>.exit` (git's
exit code). The exit file is written last, through a rename, so its presence
means the log is complete. Names may contain letters, digits, `.`, `_` and
`-`, and must be unique among the repo's live runs.

## Dashboard

```bash
commit-queue dashboard                 # http://127.0.0.1:4180
commit-queue dashboard --port 4181 --window 100
commit-queue dashboard --once          # print the bottleneck summary and exit
```

One page that refreshes every two seconds:

- **Running:** each commit step by step against typical times, with a timeline. Slots taken with `acquire` show too, though they have no log.
- **Waiting:** the line in serving order, with position, repo and wait time.
- **Recently finished:** results, time in slot, and whether the app test suites ran in parallel or one after another (`testConfig.parallel`).
- **Bottlenecks:** p50/p95 per step over the last N runs, and slot time split by test mode.

Every scan stores newly finished runs in `<state>/history.jsonl`. The first scan backfills every log on disk. `COMMIT_QUEUE_DASHBOARD_PORT` sets the default port. The page and its statistics cover every repo using the queue, each run labelled with its repo.

## How the queue works

State is plain files in one directory:

| Path | Meaning |
| --- | --- |
| `queue.lock` | Held (flock / LockFileEx) while state changes |
| `tickets/*.json` | One per waiter, with its arrival time and process id |
| `slots/slot-N.json` | One per held slot, with its owner's process id |
| `runs/<repo>/<name>.log`, `.exit` | Output and result of each `run` |
| `history.jsonl` | Finished runs, kept by the dashboard |

A waiter takes a slot only when its ticket is among the oldest N, where N is
the number of free slots, so **nobody is overtaken**. Waiters re-check every
second, so a freed slot is taken within about a second.

**Crashed callers do not block the queue.** A ticket whose waiting process
has exited is dropped, and a slot whose owner has exited is reclaimed on the
next check. A slot held longer than `staleMinutes` is reclaimed even if its
owner still looks alive, as a backstop against a reused process id.

## Scope and state directory

By default there is **one queue per machine**, because the CPU is what
commits share. A commit in one project waits behind a commit in another.

| Setting | State directory |
| --- | --- |
| `COMMIT_QUEUE_DIR` set | that directory |
| default (`scope: "machine"`) | `$XDG_STATE_HOME/claude-hooks/commit-queue`, else `~/.local/state/claude-hooks/commit-queue` (`%LOCALAPPDATA%\claude-hooks\commit-queue` on Windows) |
| `scope: "repo"` | `<git common dir>/commit-queue`, shared by the repo's worktrees |

## Configuration

`.pre-commit.json` in the worktree, all optional:

```jsonc
{
  "commitQueue": {
    // "machine" (default) or "repo"
    "scope": "machine",
    // Slots for a repo-scoped queue. A machine queue is shared by repos that
    // may disagree, so it takes --slots or COMMIT_QUEUE_SLOTS instead.
    "slots": 2,
    // Reclaim a slot held this long even if its owner looks alive. Default 60.
    "staleMinutes": 60,
    // Reject a longer subject before queueing; commitlint would only reject it
    // after the whole suite. 0 = off.
    "maxHeaderLength": 100,
    // Runs in the worktree before the commit. Each KEY=VALUE line it prints
    // is added to the commit's environment; its stderr goes to the run log.
    "setupScript": "scripts/commit-queue-setup.sh"
  }
}
```

Slot count, in order of precedence: `--slots <n>` (before the command),
`COMMIT_QUEUE_SLOTS`, `commitQueue.slots` (repo scope only), then 2.

The setup script is where project-specific environment goes. For example, a
project whose tests need a particular Node version, and whose local Convex
backend cannot move files across volumes, might print:

```bash
#!/usr/bin/env bash
echo "PATH=$HOME/.nvm/versions/node/v24.19.0/bin:$PATH"
echo "TMPDIR=$(git rev-parse --git-common-dir)/convex-tmp"
```

## Log format

The log keeps the line formats of the shell queue this replaced, so one parser
reads old and new logs:

```text
[run-commit] queued at 1792084686 pid 4242 on feat/x in /path/to/worktree
[run-commit] subject: feat(x): do the thing
[commit-queue] issue-12 waiting, position 2 of the line
[commit-slot] reclaimed stale slot-1 from issue-9 (owner pid 4100 exited)
[commit-slot] issue-12 holds slot-1 from 14:40:14
...git commit and pre-commit output...
[commit-slot] issue-12 released slot-1
```
