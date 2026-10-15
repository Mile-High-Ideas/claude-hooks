package queuedash

import (
	"math"
	"reflect"
	"strconv"
	"testing"
)

// statStep is a step for the stats fixtures: a name, a duration and a result.
type statStep struct {
	name     string
	duration *int64
	result   StepResult
}

// statRun builds a finished run that took slot 1, overridden by edit.
func statRun(id string, edit func(*Run), steps []statStep) Run {
	run := Run{
		ID: id, Name: id, State: StateFinished,
		QueuedAt: qdI64(0), Slot: qdInt(1), SlotAcquiredAt: qdI64(0),
		FinishedAt: qdI64(0), ExitCode: qdInt(0),
		Steps: []RunStep{},
	}
	if edit != nil {
		edit(&run)
	}
	for seq, s := range steps {
		result := s.result
		if result == "" {
			result = StepPassed
		}
		run.Steps = append(run.Steps, RunStep{
			Name: s.name, Seq: seq, StartedAt: qdI64(0), EndedAt: qdI64(0),
			DurationMs: s.duration, Result: result,
		})
	}
	return run
}

// suiteRun builds a passing-by-default finished run whose Tests group and
// suites carry real start and end times, so TestModeOf can tell how the
// suites ran.
func suiteRun(id string, runMs int64, suites [][3]int64, exitCode int) Run {
	start, end := suites[0][1], suites[0][2]
	for _, s := range suites {
		start = min(start, s[1])
		end = max(end, s[2])
	}
	run := statRun(id, func(r *Run) {
		r.RunMs = qdI64(runMs)
		r.ExitCode = qdInt(exitCode)
	}, nil)
	names := []string{"Tests web", "Tests backend", "Tests mobile"}
	run.Steps = append(run.Steps, RunStep{
		Name: "Tests", StartedAt: qdI64(start), EndedAt: qdI64(end),
		DurationMs: qdI64(end - start), Result: StepPassed,
	})
	for i, s := range suites {
		run.Steps = append(run.Steps, RunStep{
			Name: names[s[0]], Seq: i + 1, StartedAt: qdI64(s[1]), EndedAt: qdI64(s[2]),
			DurationMs: qdI64(s[2] - s[1]), Result: StepPassed,
		})
	}
	return run
}

// Suite indexes for suiteRun.
const (
	suiteWeb     = 0
	suiteBackend = 1
)

func floatString(v *float64) string {
	if v == nil {
		return "<nil>"
	}
	return strconv.FormatFloat(*v, 'g', -1, 64)
}

func sameFloat(got, want *float64) bool {
	if got == nil || want == nil {
		return got == want
	}
	return math.Abs(*got-*want) < 1e-9
}

func TestPercentile(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		p      float64
		want   *float64
	}{
		{"p0 is the lowest", []float64{10, 20, 30, 40}, 0, qdF64(10)},
		{"p50 interpolates", []float64{10, 20, 30, 40}, 50, qdF64(25)},
		{"p100 is the highest", []float64{10, 20, 30, 40}, 100, qdF64(40)},
		{"p95 interpolates", []float64{10, 20, 30, 40}, 95, qdF64(38.5)},
		{"does not depend on input order", []float64{40, 10, 30, 20}, 50, qdF64(25)},
		{"returns the only value of a single-element list", []float64{7}, 95, qdF64(7)},
		{"returns nil for no values", []float64{}, 50, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Percentile(tt.values, tt.p); !sameFloat(got, tt.want) {
				t.Errorf("Percentile(%v, %v) = %s, want %s", tt.values, tt.p, floatString(got), floatString(tt.want))
			}
		})
	}
}

func TestPercentileDoesNotReorderInput(t *testing.T) {
	values := []float64{40, 10, 30, 20}
	Percentile(values, 50)
	if !reflect.DeepEqual(values, []float64{40, 10, 30, 20}) {
		t.Errorf("input reordered to %v", values)
	}
}

func TestPercentileRejectsOutOfRangeP(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Percentile([1], 101) did not panic")
		}
	}()
	Percentile([]float64{1}, 101)
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name   string
		values []*float64
		want   Summary
	}{
		{
			name:   "skips missing values",
			values: []*float64{nil, qdF64(100), qdF64(300), qdF64(math.NaN()), qdF64(200)},
			want:   Summary{Count: 3, P50: qdF64(200), P95: qdF64(290), Max: qdF64(300)},
		},
		{
			name:   "reports nothing for an empty list",
			values: []*float64{},
			want:   Summary{Count: 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Summarize(tt.values)
			if got.Count != tt.want.Count || !sameFloat(got.P50, tt.want.P50) ||
				!sameFloat(got.P95, tt.want.P95) || !sameFloat(got.Max, tt.want.Max) {
				t.Errorf("Summarize = {%d %s %s %s}, want {%d %s %s %s}",
					got.Count, floatString(got.P50), floatString(got.P95), floatString(got.Max),
					tt.want.Count, floatString(tt.want.P50), floatString(tt.want.P95), floatString(tt.want.Max))
			}
		})
	}
}

func runIDs(runs []Run) []string {
	ids := []string{}
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestRecentFinished(t *testing.T) {
	tests := []struct {
		name  string
		runs  []Run
		limit int
		want  []string
	}{
		{
			name: "keeps finished runs that took a slot, newest first",
			runs: []Run{
				statRun("old", func(r *Run) { r.FinishedAt = qdI64(1) }, nil),
				statRun("new", func(r *Run) { r.FinishedAt = qdI64(3) }, nil),
				statRun("waiting", func(r *Run) { r.State = StateWaiting; r.FinishedAt = nil }, nil),
				statRun("header", func(r *Run) { r.SlotAcquiredAt = nil; r.FinishedAt = qdI64(4) }, nil),
				statRun("mid", func(r *Run) { r.FinishedAt = qdI64(2) }, nil),
			},
			limit: 2,
			want:  []string{"new", "mid"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runIDs(RecentFinished(tt.runs, tt.limit)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RecentFinished = %v, want %v", got, tt.want)
			}
		})
	}
}

// stepStatRow is the part of a StepStat the tests compare.
type stepStatRow struct {
	name  string
	count int
	p50   float64
	max   float64
	group bool
}

func TestStepStats(t *testing.T) {
	tests := []struct {
		name string
		runs []Run
		want []stepStatRow
	}{
		{
			name: "summarises each step, ignoring skipped ones, slowest p95 first",
			runs: []Run{
				statRun("a", nil, []statStep{
					{name: "Lint", duration: qdI64(100)},
					{name: "Tests", duration: qdI64(1000)},
					{name: "Tests web", duration: qdI64(900)},
					{name: "Go", duration: qdI64(0), result: StepSkipped},
				}),
				statRun("b", nil, []statStep{
					{name: "Lint", duration: qdI64(300)},
					{name: "Tests", duration: qdI64(3000)},
					{name: "Tests web", duration: qdI64(2900)},
				}),
			},
			want: []stepStatRow{
				{"Tests", 2, 2000, 3000, true},
				{"Tests web", 2, 1900, 2900, false},
				{"Lint", 2, 200, 300, false},
			},
		},
		{
			name: "keeps first-appearance order for equal p95",
			runs: []Run{
				statRun("a", nil, []statStep{
					{name: "Zeta", duration: qdI64(100)},
					{name: "Alpha", duration: qdI64(100)},
					{name: "Running", duration: qdI64(999), result: StepRunning},
				}),
			},
			want: []stepStatRow{
				{"Zeta", 1, 100, 100, false},
				{"Alpha", 1, 100, 100, false},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := []stepStatRow{}
			for _, s := range StepStats(tt.runs) {
				got = append(got, stepStatRow{s.Name, s.Count, *s.P50, *s.Max, s.Group})
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("StepStats = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeBottlenecks(t *testing.T) {
	runs := []Run{
		statRun("fast", func(r *Run) {
			r.FinishedAt, r.RunMs, r.SlotWaitMs, r.TotalMs = qdI64(3), qdI64(100), qdI64(10), qdI64(110)
			r.Repo = "upc-me"
		}, []statStep{{name: "Lint", duration: qdI64(100)}}),
		statRun("slow", func(r *Run) {
			r.FinishedAt, r.RunMs, r.SlotWaitMs, r.TotalMs = qdI64(2), qdI64(900), qdI64(30), qdI64(930)
			r.ExitCode = qdInt(1)
		}, []statStep{
			{name: "Tests", duration: qdI64(800)},
			{name: "Tests web", duration: qdI64(700)},
		}),
		statRun("outside", func(r *Run) { r.FinishedAt, r.RunMs = qdI64(1), qdI64(5000) }, nil),
	}
	result := ComputeBottlenecks(runs, 2)

	if result.Runs != 2 || result.Passed != 1 {
		t.Errorf("runs, passed = %d, %d; want 2, 1", result.Runs, result.Passed)
	}
	assertFloats(t, []floatCheck{
		{"slot wait p50", result.SlotWait.P50, qdF64(20)},
		{"total max", result.Total.Max, qdF64(930)},
	})
	type slowRow struct {
		id   string
		repo string
		step NamedDuration
	}
	got := []slowRow{}
	for _, r := range result.Slowest {
		got = append(got, slowRow{r.ID, r.Repo, *r.SlowestStep})
	}
	want := []slowRow{
		{"slow", "", NamedDuration{"Tests web", 700}},
		{"fast", "upc-me", NamedDuration{"Lint", 100}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("slowest = %v, want %v", got, want)
	}
}

// floatCheck is one named nullable float comparison.
type floatCheck struct {
	name string
	got  *float64
	want *float64
}

func assertFloats(t *testing.T, checks []floatCheck) {
	t.Helper()
	for _, c := range checks {
		if !sameFloat(c.got, c.want) {
			t.Errorf("%s = %s, want %s", c.name, floatString(c.got), floatString(c.want))
		}
	}
}

var (
	sequentialSuiteRuns = []Run{
		suiteRun("s1", 420_000, [][3]int64{{suiteWeb, 0, 240_000}, {suiteBackend, 240_000, 380_000}}, 0),
		suiteRun("s2", 400_000, [][3]int64{{suiteWeb, 0, 230_000}, {suiteBackend, 230_000, 360_000}}, 0),
	}
	parallelSuiteRuns = []Run{
		suiteRun("p1", 270_000, [][3]int64{{suiteWeb, 0, 240_000}, {suiteBackend, 0, 140_000}}, 0),
	}
)

func TestComputeTestModeStats(t *testing.T) {
	failed := suiteRun("f1", 60_000, [][3]int64{{suiteWeb, 0, 30_000}, {suiteBackend, 0, 20_000}}, 1)
	oneSuite := suiteRun("o1", 90_000, [][3]int64{{suiteWeb, 0, 60_000}}, 0)

	split := ComputeTestModeStats(append(append([]Run{}, sequentialSuiteRuns...), parallelSuiteRuns...))
	filtered := ComputeTestModeStats(append(append([]Run{}, parallelSuiteRuns...), failed, oneSuite))

	counts := []struct {
		name      string
		got, want int
	}{
		{"splits: sequential runs", split.Sequential.Runs, 2},
		{"splits: parallel runs", split.Parallel.Runs, 1},
		{"leaves out failed: parallel runs", filtered.Parallel.Runs, 1},
		{"leaves out no evidence: sequential runs", filtered.Sequential.Runs, 0},
	}
	for _, c := range counts {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	assertFloats(t, []floatCheck{
		{"splits: sequential run p50", split.Sequential.Run.P50, qdF64(410_000)},
		{"splits: sequential tests p50", split.Sequential.Tests.P50, qdF64(370_000)},
		{"splits: parallel run p50", split.Parallel.Run.P50, qdF64(270_000)},
		{"splits: parallel tests p50", split.Parallel.Tests.P50, qdF64(240_000)},
		{"leaves out failed: parallel run max", filtered.Parallel.Run.Max, qdF64(270_000)},
		{"leaves out no evidence: sequential run p50", filtered.Sequential.Run.P50, nil},
	})
}

func TestComputeBottlenecksIncludesTestModeStats(t *testing.T) {
	result := ComputeBottlenecks(append(append([]Run{}, sequentialSuiteRuns...), parallelSuiteRuns...), 10)
	if result.ByTestMode.Parallel.Runs != 1 || result.ByTestMode.Sequential.Runs != 2 {
		t.Errorf("ByTestMode runs = parallel %d, sequential %d; want 1, 2",
			result.ByTestMode.Parallel.Runs, result.ByTestMode.Sequential.Runs)
	}
}
