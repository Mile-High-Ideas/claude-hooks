package queuedash

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixture reads one of the run logs in testdata.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

// findStep returns the first parsed step with this name.
func findStep(t *testing.T, log ParsedLog, name string) ParsedStep {
	t.Helper()
	for _, step := range log.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("no step named %q", name)
	return ParsedStep{}
}

func strOrNil(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func i64(v int64) *int64 { return &v }

func assertStr(t *testing.T, what string, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %s, want %q", what, strOrNil(got), want)
	}
}

func assertNilStr(t *testing.T, what string, got *string) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %q, want nil", what, *got)
	}
}

func TestParseLogPassedCommit(t *testing.T) {
	log := ParseLog(fixture(t, "passed.log"))

	t.Run("reads the run-commit preamble", func(t *testing.T) {
		if log.QueuedAtMs == nil || *log.QueuedAtMs != 1_791_992_400_000 {
			t.Errorf("QueuedAtMs = %v", log.QueuedAtMs)
		}
		if log.PID == nil || *log.PID != 4242 {
			t.Errorf("PID = %v", log.PID)
		}
		assertStr(t, "Worktree", log.Worktree, "/work/tree one")
		assertStr(t, "Subject", log.Subject, "feat(scripts): add the commit queue dashboard")
	})

	t.Run("reads the slot and its release", func(t *testing.T) {
		if log.Slot == nil || *log.Slot != (SlotLine{Number: 2, Clock: "15:42:17"}) {
			t.Errorf("Slot = %+v", log.Slot)
		}
		if !log.Released {
			t.Error("Released = false, want true")
		}
	})

	t.Run("takes the branch and sha from the commit line", func(t *testing.T) {
		assertStr(t, "Branch", log.Branch, "feat/1019-commit-queue-dashboard")
		want := CommitLine{
			Branch:  "feat/1019-commit-queue-dashboard",
			SHA:     "07cf8f799",
			Subject: "feat(scripts): add the commit queue dashboard",
		}
		if log.Commit == nil || *log.Commit != want {
			t.Errorf("Commit = %+v, want %+v", log.Commit, want)
		}
	})

	t.Run("pairs each start line with its result line", func(t *testing.T) {
		want := ParsedStep{
			Name:       "Formatting",
			StartClock: stringPtr("15:42:20"),
			EndClock:   stringPtr("15:42:21"),
			DurationMs: i64(1700),
			Result:     StepPassed,
		}
		if got := findStep(t, log, "Formatting"); !reflect.DeepEqual(got, want) {
			t.Errorf("Formatting = %+v, want %+v", got, want)
		}
		if got := findStep(t, log, "Tests web").DurationMs; got == nil || *got != 408_000 {
			t.Errorf("Tests web duration = %v", got)
		}
		assertStr(t, "Tests note", findStep(t, log, "Tests").Note, "web")
		names := make([]string, 0, len(log.Steps))
		for _, step := range log.Steps {
			names = append(names, step.Name)
		}
		wantNames := []string{"Changelog", "Formatting", "Tests", "Tests web", "Lint vendor-sync", "SRP compliance"}
		if !reflect.DeepEqual(names, wantNames) {
			t.Errorf("step names = %v, want %v", names, wantNames)
		}
	})

	t.Run("marks warnings and skipped steps", func(t *testing.T) {
		srp := findStep(t, log, "SRP compliance")
		if srp.Result != StepWarning {
			t.Errorf("SRP result = %s", srp.Result)
		}
		if srp.DurationMs == nil || *srp.DurationMs != 6200 {
			t.Errorf("SRP duration = %v", srp.DurationMs)
		}
		assertStr(t, "SRP note", srp.Note, "453 warnings, 4589 files, warning")
		if got := findStep(t, log, "Lint vendor-sync").Result; got != StepSkipped {
			t.Errorf("Lint vendor-sync result = %s", got)
		}
	})

	t.Run("ignores warning lines that are not step results", func(t *testing.T) {
		for _, step := range log.Steps {
			if strings.HasPrefix(step.Name, "Schema") {
				t.Errorf("unexpected step %q", step.Name)
			}
		}
	})
}

func TestParseLogFailedCommitWithoutPreamble(t *testing.T) {
	log := ParseLog(fixture(t, "failed.log"))

	t.Run("has no queue time, pid or subject", func(t *testing.T) {
		if log.QueuedAtMs != nil || log.PID != nil || log.Commit != nil {
			t.Errorf("QueuedAtMs=%v PID=%v Commit=%v, want all nil", log.QueuedAtMs, log.PID, log.Commit)
		}
		assertNilStr(t, "Subject", log.Subject)
	})

	t.Run("falls back to the report directory for the branch", func(t *testing.T) {
		assertNilStr(t, "Branch", log.Branch)
		assertStr(t, "ReportSlug", log.ReportSlug, "fix-888-mobile-types")
	})

	t.Run("records failed steps with their notes", func(t *testing.T) {
		formatting := findStep(t, log, "Formatting")
		if formatting.Result != StepFailed {
			t.Errorf("Formatting result = %s", formatting.Result)
		}
		assertStr(t, "Formatting note", formatting.Note, "lint-staged failed")
		if formatting.DurationMs == nil || *formatting.DurationMs != 524 {
			t.Errorf("Formatting duration = %v", formatting.DurationMs)
		}
		assertStr(t, "Typecheck mobile note", findStep(t, log, "Typecheck mobile").Note, "34 errors")
	})

	t.Run("keeps the last output line, not the slot release", func(t *testing.T) {
		assertStr(t, "LastLine", log.LastLine, "husky - pre-commit script failed (code 1)")
	})
}

func TestParseLogPartialLogs(t *testing.T) {
	t.Run("leaves steps without a result running", func(t *testing.T) {
		log := ParseLog(fixture(t, "running-midnight.log"))
		if log.Released {
			t.Error("Released = true, want false")
		}
		tests := findStep(t, log, "Tests web")
		if tests.Result != StepRunning {
			t.Errorf("Tests web result = %s", tests.Result)
		}
		assertNilStr(t, "Tests web EndClock", tests.EndClock)
		if got := findStep(t, log, "Typecheck web").Result; got != StepPassed {
			t.Errorf("Typecheck web result = %s", got)
		}
	})

	t.Run("reads a commit still waiting for a slot", func(t *testing.T) {
		log := ParseLog(fixture(t, "waiting.log"))
		if log.Slot != nil {
			t.Errorf("Slot = %+v, want nil", log.Slot)
		}
		if len(log.Steps) != 0 {
			t.Errorf("Steps = %+v, want none", log.Steps)
		}
		assertStr(t, "Branch", log.Branch, "docs/1-readme")
	})

	t.Run("notices a header commitlint would reject", func(t *testing.T) {
		log := ParseLog(fixture(t, "header-too-long.log"))
		if !log.HeaderTooLong {
			t.Error("HeaderTooLong = false, want true")
		}
		if log.Slot != nil {
			t.Errorf("Slot = %+v, want nil", log.Slot)
		}
	})

	t.Run("records a result that has no start line", func(t *testing.T) {
		log := ParseLog("  ✅ Go tests (15:42:21, 0ms, no go files)\n")
		want := []ParsedStep{{
			Name:       "Go tests",
			EndClock:   stringPtr("15:42:21"),
			DurationMs: i64(0),
			Result:     StepPassed,
			Note:       stringPtr("no go files"),
		}}
		if !reflect.DeepEqual(log.Steps, want) {
			t.Errorf("Steps = %+v, want %+v", log.Steps, want)
		}
	})

	t.Run("accepts an empty log", func(t *testing.T) {
		log := ParseLog("")
		if log.Slot != nil {
			t.Errorf("Slot = %+v, want nil", log.Slot)
		}
		assertNilStr(t, "LastLine", log.LastLine)
	})
}

func TestParseLogLineVariants(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		wantResult StepResult
		wantNote   *string
		wantSteps  int
	}{
		{
			name:       "warning icon without its variation selector",
			text:       "  ⚠  SRP compliance (15:42:28, 6.2s, 3 warnings)\n",
			wantResult: StepWarning, wantNote: stringPtr("3 warnings"), wantSteps: 1,
		},
		{
			name:       "skipped only counts inside the parentheses",
			text:       "  ✅ Lint (15:42:21, 0ms) (skipped)\n",
			wantResult: StepPassed, wantNote: stringPtr("skipped"), wantSteps: 1,
		},
		{
			name:       "carriage returns are stripped",
			text:       "  ▶ 15:42:20 Lint\r\n  ✅ Lint (15:42:21, 1s, skipped)\r\n",
			wantResult: StepSkipped, wantNote: stringPtr("skipped"), wantSteps: 1,
		},
		{
			name:      "a warning line that is not a result",
			text:      "  ⚠  Schema snapshot is behind the migrations.\n",
			wantSteps: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := ParseLog(tc.text)
			if len(log.Steps) != tc.wantSteps {
				t.Fatalf("Steps = %+v, want %d", log.Steps, tc.wantSteps)
			}
			if tc.wantSteps == 0 {
				return
			}
			step := log.Steps[0]
			if step.Result != tc.wantResult {
				t.Errorf("Result = %s, want %s", step.Result, tc.wantResult)
			}
			if !reflect.DeepEqual(step.Note, tc.wantNote) {
				t.Errorf("Note = %s, want %s", strOrNil(step.Note), strOrNil(tc.wantNote))
			}
		})
	}
}

func TestParseLogIgnoresTheQueueWaitingLine(t *testing.T) {
	waiting := "[commit-queue] issue-1019 waiting, position 2 of the line"
	log := ParseLog(fixture(t, "waiting.log") + waiting + "\n")
	want := ParseLog(fixture(t, "waiting.log"))
	want.LastLine = stringPtr(waiting)
	if !reflect.DeepEqual(log, want) {
		t.Errorf("ParseLog = %+v, want %+v", log, want)
	}
}
