package queuedash

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// stateStep is a step for the state fixtures. A running step has no end and
// no duration; any other lasts 1000 ms.
type stateStep struct {
	name    string
	result  StepResult
	start   *int64
	derived bool
}

// stateRun builds a run named name on branch feat/<name>, overridden by edit.
func stateRun(name string, state RunState, edit func(*Run), steps []stateStep) Run {
	run := Run{ID: name + "@1", Name: name, State: state, Branch: qdStr("feat/" + name), Steps: []RunStep{}}
	if edit != nil {
		edit(&run)
	}
	for seq, s := range steps {
		step := RunStep{Name: s.name, Seq: seq, StartedAt: s.start, Result: s.result, Derived: s.derived}
		if s.result != StepRunning {
			step.EndedAt = s.start
			step.DurationMs = qdI64(1000)
		}
		run.Steps = append(run.Steps, step)
	}
	return run
}

var stateFinished = stateRun("done", StateFinished, func(r *Run) {
	r.SlotAcquiredAt, r.FinishedAt, r.ExitCode = qdI64(0), qdI64(10), qdInt(0)
	r.RunMs, r.SlotWaitMs, r.TotalMs = qdI64(10), qdI64(5), qdI64(15)
}, []stateStep{
	{"Setup (convex-gen, OpenAPI)", StepPassed, qdI64(0), true},
	{"Formatting", StepPassed, qdI64(1), false},
	{"Tests web", StepPassed, qdI64(2), false},
	{"Typecheck", StepPassed, qdI64(3), false},
})

func TestPendingSteps(t *testing.T) {
	tests := []struct {
		name    string
		run     Run
		history []Run
		want    []string
	}{
		{
			name: "lists steps the newest finished run had that this run has not started",
			run: stateRun("now", StateRunning, nil, []stateStep{
				{name: "Formatting", result: StepPassed, start: qdI64(1)},
			}),
			history: []Run{stateFinished},
			want:    []string{"Tests web", "Typecheck"},
		},
		{
			name:    "expects nothing when there is no history",
			run:     stateRun("now", StateRunning, nil, nil),
			history: []Run{},
			want:    []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PendingSteps(tt.run, tt.history); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PendingSteps = %v, want %v", got, tt.want)
			}
		})
	}
}

const stateNowMs = 100_000

// mainState is the JS suite's shared buildDashboardState fixture, with the
// waiting commits now coming from queue tickets in serving order.
func mainState() DashboardState {
	return BuildDashboardState(StateInput{
		Runs: []Run{
			stateRun("b-waiting", StateWaiting, func(r *Run) { r.QueuedAt = qdI64(40_000) }, nil),
			stateRun("a-waiting", StateWaiting, func(r *Run) { r.QueuedAt = qdI64(20_000) }, nil),
			stateRun("runner", StateRunning, func(r *Run) { r.Slot, r.SlotAcquiredAt = qdInt(2), qdI64(70_000) },
				[]stateStep{
					{name: "Formatting", result: StepPassed, start: qdI64(70_500)},
					{name: "Tests web", result: StepRunning, start: qdI64(80_000)},
				}),
			stateRun("ghost", StateAbandoned, func(r *Run) { r.SlotAcquiredAt = qdI64(10_000) }, nil),
			stateFinished,
		},
		Slots: []QueueSlot{{Slot: 2, Owner: "runner", StartedAt: 70_000}},
		Tickets: []QueueTicket{
			{Name: "a-waiting", QueuedAt: 20_000},
			{Name: "b-waiting", QueuedAt: 40_000},
		},
		Recent:        []Run{stateFinished},
		WindowRuns:    []Run{stateFinished},
		NowMs:         stateNowMs,
		SlotCount:     2,
		HistoryCounts: HistoryCounts{Runs: 1, Steps: 4},
	})
}

func findLiveStep(steps []LiveStep, name string) LiveStep {
	for _, s := range steps {
		if s.Name == name {
			return s
		}
	}
	return LiveStep{}
}

func TestBuildDashboardStateWaitingFromTickets(t *testing.T) {
	type row struct {
		position int
		name     string
		waitMs   int64
		branch   string
	}
	got := []row{}
	for _, w := range mainState().Waiting {
		got = append(got, row{w.Position, w.Name, w.WaitMs, *w.Branch})
	}
	want := []row{
		{1, "a-waiting", 80_000, "feat/a-waiting"},
		{2, "b-waiting", 60_000, "feat/b-waiting"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("waiting = %v, want %v", got, want)
	}
}

func TestBuildDashboardStateRunning(t *testing.T) {
	state := mainState()
	if len(state.Running) != 1 {
		t.Fatalf("running = %d, want 1", len(state.Running))
	}
	current := state.Running[0]
	if current.ElapsedMs == nil || *current.ElapsedMs != 30_000 {
		t.Errorf("elapsed = %v, want 30000", current.ElapsedMs)
	}
	tests := findLiveStep(current.Steps, "Tests web")
	if tests.ElapsedMs == nil || *tests.ElapsedMs != 20_000 {
		t.Errorf("Tests web elapsed = %v, want 20000", tests.ElapsedMs)
	}
	assertFloats(t, []floatCheck{{"Tests web typical", tests.TypicalMs, qdF64(1000)}})
	if len(current.Pending) != 1 || current.Pending[0].Name != "Typecheck" {
		t.Errorf("pending = %v, want [Typecheck]", current.Pending)
	}
}

func TestBuildDashboardStateCoversTheWholeWindow(t *testing.T) {
	// The server fetches the window as slot-taking runs only, so none of
	// them is dropped here.
	many := []Run{}
	for i := range 3 {
		many = append(many, stateRun("w"+string(rune('0'+i)), StateFinished, func(r *Run) {
			r.SlotAcquiredAt, r.FinishedAt = qdI64(int64(i)), qdI64(int64(10+i))
			r.ExitCode, r.RunMs = qdInt(0), qdI64(10)
		}, nil))
	}
	windowed := BuildDashboardState(StateInput{
		WindowRuns: many, NowMs: stateNowMs, SlotCount: 2,
		HistoryCounts: HistoryCounts{Runs: 3},
	})
	if windowed.Bottlenecks.Runs != 3 {
		t.Errorf("bottleneck runs = %d, want 3", windowed.Bottlenecks.Runs)
	}
	if len(windowed.Recent) != 0 {
		t.Errorf("recent = %v, want empty", windowed.Recent)
	}
}

func TestBuildDashboardStatePassesThrough(t *testing.T) {
	state := mainState()
	if len(state.Abandoned) != 1 || state.Abandoned[0].Name != "ghost" {
		t.Errorf("abandoned = %v, want [ghost]", state.Abandoned)
	}
	if len(state.Recent) != 1 || state.Recent[0].Name != "done" {
		t.Errorf("recent = %v, want [done]", state.Recent)
	}
	if state.Bottlenecks.Runs != 1 || state.SlotCount != 2 || state.GeneratedAt != stateNowMs {
		t.Errorf("runs, slotCount, generatedAt = %d, %d, %d; want 1, 2, %d",
			state.Bottlenecks.Runs, state.SlotCount, state.GeneratedAt, stateNowMs)
	}
	if state.History != (HistoryCounts{Runs: 1, Steps: 4}) {
		t.Errorf("history = %v, want {1 4}", state.History)
	}
	if len(state.HeldWithoutRun) != 0 {
		t.Errorf("heldWithoutRun = %v, want none: runner's slot has its run", state.HeldWithoutRun)
	}
}

// timedSuite is a suite step; a nil end means it is still running.
type timedSuite struct {
	name  string
	start int64
	end   *int64
}

// timedRun builds a run whose suites carry real start and end times.
func timedRun(name string, state RunState, suites []timedSuite) Run {
	run := stateRun(name, state, func(r *Run) {
		r.SlotAcquiredAt = qdI64(0)
		if state == StateFinished {
			r.FinishedAt, r.ExitCode = qdI64(500_000), qdInt(0)
		}
	}, nil)
	for seq, s := range suites {
		step := RunStep{Name: s.name, Seq: seq, StartedAt: qdI64(s.start), EndedAt: s.end, Result: StepRunning}
		if s.end != nil {
			step.DurationMs = qdI64(*s.end - s.start)
			step.Result = StepPassed
		}
		run.Steps = append(run.Steps, step)
	}
	return run
}

var (
	sequentialPast = timedRun("seq", StateFinished, []timedSuite{
		{"Tests web", 0, qdI64(240_000)},
		{"Tests backend", 240_000, qdI64(380_000)},
		{"Tests", 0, qdI64(380_000)},
	})
	parallelPast = timedRun("par", StateFinished, []timedSuite{
		{"Tests web", 0, qdI64(250_000)},
		{"Tests backend", 0, qdI64(140_000)},
		{"Tests", 0, qdI64(250_000)},
	})
	modeHistory    = []Run{parallelPast, sequentialPast}
	liveBothSuites = timedRun("live", StateRunning, []timedSuite{
		{"Tests web", 10_000, nil},
		{"Tests backend", 10_000, nil},
	})
)

func TestTypicalTimes(t *testing.T) {
	tests := []struct {
		name    string
		history []Run
		mode    *TestMode
		want    *float64
	}{
		{"measures a parallel run against parallel runs only", modeHistory, qdMode(TestModeParallel), qdF64(250_000)},
		{"measures a sequential run against sequential runs only", modeHistory, qdMode(TestModeSequential), qdF64(380_000)},
		{"uses every run when no run matches the mode", []Run{sequentialPast}, qdMode(TestModeParallel), qdF64(380_000)},
		{"uses every run when there is no mode", modeHistory, nil, qdF64(315_000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TypicalTimes(tt.history, tt.mode)["Tests"]
			assertFloats(t, []floatCheck{{"Tests typical", got, tt.want}})
		})
	}
}

func TestExpectedTestMode(t *testing.T) {
	lintOnly := timedRun("live", StateRunning, []timedSuite{{"Lint web", 0, qdI64(5000)}})
	tests := []struct {
		name    string
		run     Run
		history []Run
		want    *TestMode
	}{
		{"takes a live run's mode from its own suites once two are running", liveBothSuites, []Run{sequentialPast}, qdMode(TestModeParallel)},
		{"expects the newest finished run's mode before the suites start", lintOnly, modeHistory, qdMode(TestModeParallel)},
		{"has no expectation without history", lintOnly, []Run{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpectedTestMode(tt.run, tt.history); modeString(got) != modeString(tt.want) {
				t.Errorf("ExpectedTestMode = %s, want %s", modeString(got), modeString(tt.want))
			}
		})
	}
}

func liveState(live Run) DashboardState {
	return BuildDashboardState(StateInput{
		Runs: []Run{live}, Recent: modeHistory, WindowRuns: modeHistory,
		NowMs: stateNowMs, SlotCount: 2, HistoryCounts: HistoryCounts{Runs: 2, Steps: 6},
	})
}

// TestBuildDashboardStateTestModeBadge checks that the badge shows only a
// run's own evidence, while typical times follow the best guess. Each
// worktree reads testConfig.parallel from its own branch, so a finished
// parallel run says nothing certain about a live one.
func TestBuildDashboardStateTestModeBadge(t *testing.T) {
	tests := []struct {
		name string
		live Run
		want *TestMode
	}{
		{"labels a running run with its own mode", liveBothSuites, qdMode(TestModeParallel)},
		{
			"does not label a live run from another run before its own suites show a mode",
			timedRun("live", StateRunning, []timedSuite{{"Tests web", 10_000, nil}}),
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := liveState(tt.live).Running[0]
			if modeString(current.TestMode) != modeString(tt.want) {
				t.Errorf("testMode = %s, want %s", modeString(current.TestMode), modeString(tt.want))
			}
			typical := findLiveStep(current.Steps, "Tests web").TypicalMs
			assertFloats(t, []floatCheck{{"Tests web typical", typical, qdF64(250_000)}})
		})
	}
}

func TestBuildDashboardStateLabelsRecentRuns(t *testing.T) {
	got := []string{}
	for _, r := range liveState(liveBothSuites).Recent {
		got = append(got, r.Name+"="+modeString(r.TestMode))
	}
	want := []string{"par=parallel", "seq=sequential"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recent = %v, want %v", got, want)
	}
}

func TestBuildDashboardStateWaitingEnrichment(t *testing.T) {
	state := BuildDashboardState(StateInput{
		Runs: []Run{
			stateRun("same-name", StateWaiting, func(r *Run) {
				r.RepoKey, r.Subject, r.Worktree = "repo-b", qdStr("fix: b"), qdStr("/wt/b")
			}, nil),
			stateRun("running-one", StateRunning, func(r *Run) { r.RepoKey = "repo-a" }, nil),
		},
		Tickets: []QueueTicket{
			{Name: "late", RepoKey: "repo-a", Repo: "a", QueuedAt: 90_000},
			{Name: "same-name", RepoKey: "repo-a", Repo: "a", QueuedAt: 10_000},
			{Name: "same-name", RepoKey: "repo-b", Repo: "b", QueuedAt: 20_000},
			{Name: "running-one", RepoKey: "repo-a", Repo: "a", QueuedAt: 30_000},
			{Name: "future", RepoKey: "repo-a", Repo: "a", QueuedAt: stateNowMs + 5000},
		},
		NowMs: stateNowMs,
	})
	tests := []struct {
		position int
		name     string
		repo     string
		waitMs   int64
		subject  string
		worktree string
	}{
		{1, "late", "a", 10_000, "", ""},
		{2, "same-name", "a", 90_000, "", ""},
		{3, "same-name", "b", 80_000, "fix: b", "/wt/b"},
		{4, "running-one", "a", 70_000, "", ""},
		{5, "future", "a", 0, "", ""},
	}
	if len(state.Waiting) != len(tests) {
		t.Fatalf("waiting = %d, want %d", len(state.Waiting), len(tests))
	}
	for i, tt := range tests {
		w := state.Waiting[i]
		t.Run(tt.name+"@"+tt.repo, func(t *testing.T) {
			if w.Position != tt.position || w.Name != tt.name || w.Repo != tt.repo || w.WaitMs != tt.waitMs {
				t.Errorf("got {%d %s %s %d}, want {%d %s %s %d}",
					w.Position, w.Name, w.Repo, w.WaitMs, tt.position, tt.name, tt.repo, tt.waitMs)
			}
			if strOrEmpty(w.Subject) != tt.subject || strOrEmpty(w.Worktree) != tt.worktree {
				t.Errorf("subject, worktree = %q, %q; want %q, %q",
					strOrEmpty(w.Subject), strOrEmpty(w.Worktree), tt.subject, tt.worktree)
			}
			if tt.subject == "" && w.Branch != nil {
				t.Errorf("branch = %q, want nil without a matching waiting run", *w.Branch)
			}
		})
	}
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestBuildDashboardStateHeldWithoutRun(t *testing.T) {
	state := BuildDashboardState(StateInput{
		Runs: []Run{
			stateRun("runner", StateRunning, func(r *Run) { r.RepoKey = "repo-a" }, nil),
		},
		Slots: []QueueSlot{
			{Slot: 1, Owner: "runner", RepoKey: "repo-a"},
			{Slot: 2, Owner: "manual", RepoKey: "repo-a"},
			{Slot: 3, Owner: "runner", RepoKey: "repo-b"},
		},
		NowMs: stateNowMs,
	})
	got := []int{}
	for _, s := range state.HeldWithoutRun {
		got = append(got, s.Slot)
	}
	if want := []int{2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("heldWithoutRun slots = %v, want %v", got, want)
	}
	if len(state.Slots) != 3 {
		t.Errorf("slots = %d, want all 3 passed through", len(state.Slots))
	}
}

func TestBuildDashboardStateEmptyListsMarshalAsArrays(t *testing.T) {
	empty := BuildDashboardState(StateInput{NowMs: stateNowMs})
	withRun := BuildDashboardState(StateInput{
		Runs:  []Run{{ID: "bare@1", Name: "bare", State: StateRunning}},
		NowMs: stateNowMs,
	})
	tests := []struct {
		name  string
		state DashboardState
		want  []string
	}{
		{
			name:  "empty state",
			state: empty,
			want: []string{
				`"slots":[]`, `"running":[]`, `"heldWithoutRun":[]`, `"abandoned":[]`,
				`"waiting":[]`, `"recent":[]`, `"steps":[]`, `"slowest":[]`,
			},
		},
		{
			name:  "running run with no steps and no history",
			state: withRun,
			want:  []string{`"steps":[]`, `"pending":[]`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.state)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			text := string(raw)
			for _, fragment := range tt.want {
				if !strings.Contains(text, fragment) {
					t.Errorf("JSON lacks %s: %s", fragment, text)
				}
			}
		})
	}
}
