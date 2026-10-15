// Command commit-queue limits how many pre-commit suites run on a machine
// at once and serves waiting commits strictly first come, first served.
//
//	commit-queue run <name> <worktree> <git commit args...>
//	commit-queue acquire <name> [--pid <owner>]
//	commit-queue release <name>
//	commit-queue status [--json]
//	commit-queue wait [--all] [--poll <duration>]
//
// `run` is the usual entry point. Start it detached so it outlives the
// agent that queued it:
//
//	nohup commit-queue run issue-12 "$PWD" -F msg.txt >/dev/null 2>&1 &
//
// It writes <state>/runs/<repo>/<name>.log and then <name>.exit. `wait`
// blocks until a run in this repo finishes. The state directory is shared by
// every repo on the machine unless .pre-commit.json sets
// commitQueue.scope to "repo"; COMMIT_QUEUE_DIR overrides it.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

const usage = `usage:
  commit-queue run <name> <worktree> <git commit args...>
  commit-queue acquire <name> [--pid <owner pid>]
  commit-queue release <name>
  commit-queue status [--json]
  commit-queue wait [--all] [--poll <duration>]

Global flag (before the command): --slots <n>, or COMMIT_QUEUE_SLOTS.
`

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr))
}

func dispatch(args []string, out, errOut io.Writer) int {
	global := flag.NewFlagSet("commit-queue", flag.ContinueOnError)
	global.SetOutput(errOut)
	slots := global.Int("slots", 0, "slots in the queue (default: COMMIT_QUEUE_SLOTS, then 2)")
	global.Usage = func() { say(errOut, "%s", usage) }
	if err := global.Parse(args); err != nil {
		return 2
	}
	rest := global.Args()
	if len(rest) == 0 {
		say(errOut, "%s", usage)
		return 2
	}
	cmd, cmdArgs := rest[0], rest[1:]
	switch cmd {
	case "run":
		// git's own flags follow, so they are not parsed here.
		if len(cmdArgs) < 2 {
			say(errOut, "%s", usage)
			return 2
		}
		return runCommit(cmdArgs[0], cmdArgs[1], cmdArgs[2:], *slots)
	case "acquire":
		return runAcquire(cmdArgs, *slots, out, errOut)
	case "release":
		if len(cmdArgs) != 1 {
			say(errOut, "%s", usage)
			return 2
		}
		return releaseSlot(cmdArgs[0], out)
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		fs.SetOutput(errOut)
		asJSON := fs.Bool("json", false, "print JSON")
		if err := fs.Parse(cmdArgs); err != nil {
			return 2
		}
		return printStatus(*asJSON, out)
	case "wait":
		fs := flag.NewFlagSet("wait", flag.ContinueOnError)
		fs.SetOutput(errOut)
		all := fs.Bool("all", false, "wait for a run in any repo, not just this one")
		poll := fs.Duration("poll", 5*time.Second, "how often to check")
		if err := fs.Parse(cmdArgs); err != nil {
			return 2
		}
		return waitForExits(*all, *poll, out)
	default:
		say(errOut, "unknown command %q\n%s", cmd, usage)
		return 2
	}
}

// say writes to a log or terminal. A failed write there cannot be reported
// anywhere more useful, so its error is dropped on purpose.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func runAcquire(args []string, slots int, out, errOut io.Writer) int {
	if len(args) == 0 {
		say(errOut, "%s", usage)
		return 2
	}
	fs := flag.NewFlagSet("acquire", flag.ContinueOnError)
	fs.SetOutput(errOut)
	pid := fs.Int("pid", 0, "process that owns the slot (default: the calling shell)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	return acquireSlot(args[0], *pid, slots, out)
}
