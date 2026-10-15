package queuedash

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// utc parses "2006-01-02T15:04:05[.000]" as UTC Unix milliseconds.
func utc(t *testing.T, text string) int64 {
	t.Helper()
	parsed, err := time.Parse("2006-01-02T15:04:05.999", text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return parsed.UnixMilli()
}

// runInput is the default input the cases override, read in UTC.
func runInput(t *testing.T) RunInput {
	t.Helper()
	useClockLocation(t, "UTC")
	return RunInput{
		Name:          "issue-1019",
		LogMtimeMs:    utc(t, "2026-10-14T15:49:11"),
		NowMs:         utc(t, "2026-10-14T15:50:00"),
		IsRunnerAlive: func(int) bool { return true },
	}
}

// replaceFirst replaces the first match of pattern, as String.replace does
// with a non-global regex.
func replaceFirst(text, pattern, with string) string {
	loc := regexp.MustCompile(pattern).FindStringIndex(text)
	if loc == nil {
		return text
	}
	return text[:loc[0]] + with + text[loc[1]:]
}

func passedRun(t *testing.T) Run {
	t.Helper()
	in := runInput(t)
	in.Text = fixture(t, "passed.log")
	in.Exit = &ExitInfo{Code: 0, MtimeMs: utc(t, "2026-10-14T15:49:12.4")}
	return BuildRun(in)
}

func findRunStep(t *testing.T, run Run, name string) RunStep {
	t.Helper()
	for _, step := range run.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("no run step named %q", name)
	return RunStep{}
}

func assertI64(t *testing.T, what string, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %d", what, ptrString(got), want)
	}
}

func assertNilI64(t *testing.T, what string, got *int64) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %d, want nil", what, *got)
	}
}

func ptrString(v *int64) string {
	if v == nil {
		return "<nil>"
	}
	return time.UnixMilli(*v).UTC().Format(time.RFC3339Nano)
}

func TestBuildRunFinishedRun(t *testing.T) {
	t.Run("resolves every stamp against the logged queue time", func(t *testing.T) {
		run := passedRun(t)
		if run.State != StateFinished {
			t.Errorf("State = %s", run.State)
		}
		assertI64(t, "QueuedAt", run.QueuedAt, utc(t, "2026-10-14T15:40:00"))
		assertI64(t, "SlotAcquiredAt", run.SlotAcquiredAt, utc(t, "2026-10-14T15:42:17"))
		assertI64(t, "FinishedAt", run.FinishedAt, utc(t, "2026-10-14T15:49:12.4"))
		assertI64(t, "SlotWaitMs", run.SlotWaitMs, 137_000)
		assertI64(t, "RunMs", run.RunMs, 415_400)
		assertI64(t, "TotalMs", run.TotalMs, 552_400)
	})

	t.Run("keys the run by name and slot time", func(t *testing.T) {
		want := "issue-1019@1791992537000"
		if got := passedRun(t).ID; got != want {
			t.Errorf("ID = %q, want %q", got, want)
		}
	})

	t.Run("adds the unstamped setup and finish stretches around the checks", func(t *testing.T) {
		run := passedRun(t)
		setup := run.Steps[0]
		if setup.Name != SetupStep || !setup.Derived {
			t.Errorf("first step = %+v", setup)
		}
		assertI64(t, "setup duration", setup.DurationMs, 3000)
		finish := run.Steps[len(run.Steps)-1]
		if finish.Name != FinishStep {
			t.Errorf("last step = %q", finish.Name)
		}
		assertI64(t, "finish start", finish.StartedAt, utc(t, "2026-10-14T15:49:10"))
		assertI64(t, "finish duration", finish.DurationMs, 2400)
		for i, step := range run.Steps {
			if step.Seq != i {
				t.Errorf("step %d (%s) has seq %d", i, step.Name, step.Seq)
			}
		}
	})

	t.Run("keeps the printed duration over the second-truncated stamps", func(t *testing.T) {
		formatting := findRunStep(t, passedRun(t), "Formatting")
		assertI64(t, "StartedAt", formatting.StartedAt, utc(t, "2026-10-14T15:42:20"))
		assertI64(t, "EndedAt", formatting.EndedAt, utc(t, "2026-10-14T15:42:21"))
		assertI64(t, "DurationMs", formatting.DurationMs, 1700)
	})

	t.Run("reports no failure for a passed run", func(t *testing.T) {
		run := passedRun(t)
		assertNilStr(t, "Failure", run.Failure)
		assertStr(t, "SHA", run.SHA, "07cf8f799")
	})
}

func TestBuildRunFailedRunWithoutPreamble(t *testing.T) {
	failed := func(t *testing.T, logBirthMs *int64) Run {
		t.Helper()
		in := runInput(t)
		in.Name = "issue-888"
		in.Text = fixture(t, "failed.log")
		in.LogBirthMs = logBirthMs
		in.LogMtimeMs = utc(t, "2026-10-13T11:47:53")
		in.Exit = &ExitInfo{Code: 1, MtimeMs: utc(t, "2026-10-13T11:47:53.2")}
		return BuildRun(in)
	}

	t.Run("uses the log birth time as the queue time", func(t *testing.T) {
		run := failed(t, i64(utc(t, "2026-10-13T11:30:00")))
		assertI64(t, "QueuedAt", run.QueuedAt, utc(t, "2026-10-13T11:30:00"))
		assertI64(t, "SlotWaitMs", run.SlotWaitMs, 17*60_000+40_000)
	})

	t.Run("works backward from the exit file when the birth time is unusable", func(t *testing.T) {
		// A copied file can report a birth time after its last write.
		run := failed(t, i64(utc(t, "2026-10-14T09:00:00")))
		assertNilI64(t, "QueuedAt", run.QueuedAt)
		assertI64(t, "SlotAcquiredAt", run.SlotAcquiredAt, utc(t, "2026-10-13T11:47:40"))
		assertNilI64(t, "SlotWaitMs", run.SlotWaitMs)
	})

	t.Run("names the innermost failed step", func(t *testing.T) {
		run := failed(t, nil)
		if run.ExitCode == nil || *run.ExitCode != 1 {
			t.Errorf("ExitCode = %v", run.ExitCode)
		}
		assertStr(t, "Failure", run.Failure, "Formatting: lint-staged failed")
		assertStr(t, "Branch", run.Branch, "fix-888-mobile-types")
	})

	t.Run("prefers a child step over the group it failed", func(t *testing.T) {
		in := runInput(t)
		in.Text = regexp.MustCompile(`.*Formatting.*\n`).ReplaceAllString(fixture(t, "failed.log"), "")
		in.Exit = &ExitInfo{Code: 1, MtimeMs: utc(t, "2026-10-13T11:47:53")}
		assertStr(t, "Failure", BuildRun(in).Failure, "Typecheck mobile: 34 errors")
	})

	t.Run("blames commitlint when every check passed but the commit failed", func(t *testing.T) {
		in := runInput(t)
		in.Text = replaceFirst(fixture(t, "passed.log"), `\[feat/1019[^\n]*\n[^\n]*\n`,
			"husky - commit-msg script failed (code 1)\n")
		in.Exit = &ExitInfo{Code: 1, MtimeMs: utc(t, "2026-10-14T15:49:12")}
		run := BuildRun(in)
		assertStr(t, "Failure", run.Failure, "husky - commit-msg script failed (code 1)")
		if last := run.Steps[len(run.Steps)-1]; last.Result != StepFailed {
			t.Errorf("last step %s result = %s, want failed", last.Name, last.Result)
		}
	})
}

func TestBuildRunRunsInProgress(t *testing.T) {
	beforeChecks := func(t *testing.T) string {
		return strings.SplitN(fixture(t, "passed.log"), "Running pre-commit checks", 2)[0]
	}

	t.Run("places stamps after midnight on the next day", func(t *testing.T) {
		in := runInput(t)
		in.Name = "late-7"
		in.Text = fixture(t, "running-midnight.log")
		in.SlotHeld = true
		in.NowMs = utc(t, "2026-10-15T00:01:00")
		run := BuildRun(in)
		if run.State != StateRunning {
			t.Errorf("State = %s", run.State)
		}
		assertI64(t, "SlotAcquiredAt", run.SlotAcquiredAt, utc(t, "2026-10-14T23:58:30"))
		typecheck := findRunStep(t, run, "Typecheck web")
		assertI64(t, "Typecheck StartedAt", typecheck.StartedAt, utc(t, "2026-10-15T00:00:05"))
		assertI64(t, "Typecheck EndedAt", typecheck.EndedAt, utc(t, "2026-10-15T00:00:20"))
		tests := findRunStep(t, run, "Tests web")
		if tests.Result != StepRunning {
			t.Errorf("Tests web result = %s", tests.Result)
		}
		assertNilI64(t, "Tests web DurationMs", tests.DurationMs)
		for _, step := range run.Steps {
			if step.Name == FinishStep {
				t.Error("unexpected finish step while a check is running")
			}
		}
	})

	t.Run("shows setup as running between taking the slot and the first check", func(t *testing.T) {
		in := runInput(t)
		in.Text = beforeChecks(t)
		in.SlotHeld = true
		in.NowMs = utc(t, "2026-10-14T15:42:40")
		run := BuildRun(in)
		if run.State != StateRunning {
			t.Errorf("State = %s", run.State)
		}
		want := []RunStep{{
			Name:      SetupStep,
			StartedAt: i64(utc(t, "2026-10-14T15:42:17")),
			Result:    StepRunning,
			Derived:   true,
		}}
		if !reflect.DeepEqual(run.Steps, want) {
			t.Errorf("Steps = %+v, want %+v", run.Steps, want)
		}
	})

	t.Run("shows finish as running after the last check until the exit file", func(t *testing.T) {
		in := runInput(t)
		in.Text = strings.SplitN(fixture(t, "passed.log"), "\n================", 2)[0]
		in.SlotHeld = true
		run := BuildRun(in)
		finish := run.Steps[len(run.Steps)-1]
		if finish.Name != FinishStep || finish.Result != StepRunning {
			t.Errorf("last step = %s %s, want running finish", finish.Name, finish.Result)
		}
		assertI64(t, "finish StartedAt", finish.StartedAt, utc(t, "2026-10-14T15:49:10"))
		assertNilI64(t, "finish DurationMs", finish.DurationMs)
	})

	t.Run("fails setup when the commit ended before any check started", func(t *testing.T) {
		in := runInput(t)
		in.Text = beforeChecks(t)
		in.Exit = &ExitInfo{Code: 1, MtimeMs: utc(t, "2026-10-14T15:42:30")}
		run := BuildRun(in)
		if len(run.Steps) != 1 {
			t.Fatalf("Steps = %+v, want only setup", run.Steps)
		}
		setup := run.Steps[0]
		if setup.Name != SetupStep || setup.Result != StepFailed {
			t.Errorf("setup = %s %s, want failed setup", setup.Name, setup.Result)
		}
		assertI64(t, "setup DurationMs", setup.DurationMs, 13_000)
	})

	t.Run("is waiting until it holds a slot", func(t *testing.T) {
		in := runInput(t)
		in.Text = fixture(t, "waiting.log")
		run := BuildRun(in)
		if run.State != StateWaiting {
			t.Errorf("State = %s", run.State)
		}
		assertStr(t, "Subject", run.Subject, "docs: explain the queue")
		if want := "issue-1019@1791992400000"; run.ID != want {
			t.Errorf("ID = %q, want %q", run.ID, want)
		}
	})

	t.Run("is abandoned when its runner process is gone", func(t *testing.T) {
		in := runInput(t)
		in.Text = fixture(t, "waiting.log")
		var seen []int
		in.IsRunnerAlive = func(pid int) bool {
			seen = append(seen, pid)
			return false
		}
		run := BuildRun(in)
		if !reflect.DeepEqual(seen, []int{6262}) {
			t.Errorf("asked about pids %v, want [6262]", seen)
		}
		if run.State != StateAbandoned {
			t.Errorf("State = %s", run.State)
		}
	})

	t.Run("stays running after its slot was reclaimed while the runner lives", func(t *testing.T) {
		in := runInput(t)
		in.Text = fixture(t, "running-midnight.log")
		if got := BuildRun(in).State; got != StateRunning {
			t.Errorf("State = %s", got)
		}
	})

	t.Run("judges a log with no pid by how long ago it was written", func(t *testing.T) {
		in := runInput(t)
		in.Text = replaceFirst(fixture(t, "failed.log"), `.*released.*\n`, "")
		in.LogMtimeMs = utc(t, "2026-10-13T11:47:53")
		in.NowMs = utc(t, "2026-10-13T11:48:30")
		if got := BuildRun(in).State; got != StateRunning {
			t.Errorf("fresh State = %s", got)
		}
		in.NowMs = utc(t, "2026-10-13T12:30:00")
		if got := BuildRun(in).State; got != StateAbandoned {
			t.Errorf("stale State = %s", got)
		}
	})

	t.Run("gives a re-queued name a new id", func(t *testing.T) {
		first := passedRun(t)
		in := runInput(t)
		in.Text = strings.Replace(fixture(t, "passed.log"), "15:42:17", "16:10:00", 1)
		in.Exit = &ExitInfo{Code: 0, MtimeMs: utc(t, "2026-10-14T16:20:00")}
		if second := BuildRun(in); first.ID == second.ID {
			t.Errorf("both runs have id %q", first.ID)
		}
	})
}

func TestBuildRunCarriesTheRepo(t *testing.T) {
	cases := []struct {
		name    string
		repoKey string
		repo    string
		wantID  string
	}{
		{"per-repo shell queue", "", "", "issue-1019@1791992400000"},
		{"machine-wide Go queue", "a1b2c3", "upc-me", "a1b2c3/issue-1019@1791992400000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := runInput(t)
			in.Text = fixture(t, "waiting.log")
			in.RepoKey = tc.repoKey
			in.Repo = tc.repo
			run := BuildRun(in)
			if run.ID != tc.wantID || run.RepoKey != tc.repoKey || run.Repo != tc.repo {
				t.Errorf("ID=%q RepoKey=%q Repo=%q, want %q %q %q",
					run.ID, run.RepoKey, run.Repo, tc.wantID, tc.repoKey, tc.repo)
			}
		})
	}
}
