package queuedash

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// slowestRunsShown is how many rows the slowest-runs table holds.
const slowestRunsShown = 10

// Percentile returns the p-th percentile (0-100) of values by linear
// interpolation between closest ranks, the same definition as numpy's default
// and Excel's PERCENTILE.INC. It returns nil for no values and panics when p
// is outside 0..100, which is a programming error. values is not reordered.
func Percentile(values []float64, p float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	if p < 0 || p > 100 {
		panic(fmt.Sprintf("percentile out of range: %v", p))
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := p / 100 * float64(len(sorted)-1)
	low := int(math.Floor(rank))
	high := int(math.Ceil(rank))
	result := sorted[low] + (sorted[high]-sorted[low])*(rank-float64(low))
	return &result
}

// Summarize returns the count, p50, p95 and max of values, skipping nil and
// non-finite ones.
func Summarize(values []*float64) Summary {
	present := make([]float64, 0, len(values))
	for _, v := range values {
		if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
			continue
		}
		present = append(present, *v)
	}
	summary := Summary{
		Count: len(present),
		P50:   Percentile(present, 50),
		P95:   Percentile(present, 95),
	}
	if len(present) > 0 {
		highest := present[0]
		for _, v := range present[1:] {
			highest = math.Max(highest, v)
		}
		summary.Max = &highest
	}
	return summary
}

// RecentFinished returns finished runs that took a slot, newest first, at
// most limit of them.
func RecentFinished(runs []Run, limit int) []Run {
	finished := make([]Run, 0, len(runs))
	for _, r := range runs {
		if r.State == StateFinished && r.SlotAcquiredAt != nil {
			finished = append(finished, r)
		}
	}
	sort.SliceStable(finished, func(a, b int) bool {
		return statMsOrZero(finished[a].FinishedAt) > statMsOrZero(finished[b].FinishedAt)
	})
	if limit < len(finished) {
		finished = finished[:max(0, limit)]
	}
	return finished
}

// stepDurations collects one step name's durations across runs.
type stepDurations struct {
	values  []float64
	derived bool
}

// StepStats returns per-step duration percentiles, slowest p95 first, ties
// in the order the steps first appear. A step is a group when other steps
// are named after it ("Tests" holds "Tests web"): its time overlaps theirs,
// so it is flagged rather than mixed in. Skipped, running and untimed steps
// are left out.
func StepStats(runs []Run) []StepStat {
	names, byName := collectStepDurations(runs)
	stats := make([]StepStat, 0, len(names))
	for _, name := range names {
		entry := byName[name]
		stats = append(stats, StepStat{
			Name:    name,
			Derived: entry.derived,
			Group:   isGroup(name, names),
			Summary: Summarize(statFloatRefs(entry.values)),
		})
	}
	sort.SliceStable(stats, func(a, b int) bool {
		return statFloatOrZero(stats[a].P95) > statFloatOrZero(stats[b].P95)
	})
	return stats
}

// collectStepDurations groups timed step durations by name, returning the
// names in first-appearance order.
func collectStepDurations(runs []Run) ([]string, map[string]*stepDurations) {
	var names []string
	byName := map[string]*stepDurations{}
	for _, run := range runs {
		for _, step := range run.Steps {
			if step.Result == StepSkipped || step.Result == StepRunning || step.DurationMs == nil {
				continue
			}
			entry, ok := byName[step.Name]
			if !ok {
				entry = &stepDurations{derived: step.Derived}
				byName[step.Name] = entry
				names = append(names, step.Name)
			}
			entry.values = append(entry.values, float64(*step.DurationMs))
		}
	}
	return names, byName
}

// isGroup reports whether any of names is a step inside name.
func isGroup(name string, names []string) bool {
	prefix := name + " "
	for _, other := range names {
		if strings.HasPrefix(other, prefix) {
			return true
		}
	}
	return false
}

// slowestLeafStep returns the slowest step of a run that is not a group of
// other steps, or nil when no step is timed.
func slowestLeafStep(run Run) *NamedDuration {
	names := make([]string, 0, len(run.Steps))
	for _, s := range run.Steps {
		names = append(names, s.Name)
	}
	var slowest *NamedDuration
	for _, step := range run.Steps {
		if step.DurationMs == nil || isGroup(step.Name, names) {
			continue
		}
		if slowest == nil || *step.DurationMs > slowest.DurationMs {
			slowest = &NamedDuration{Name: step.Name, DurationMs: *step.DurationMs}
		}
	}
	return slowest
}

// ComputeTestModeStats returns time in the slot and in the Tests phase for
// passing runs, split by whether their test suites ran one after another or
// at the same time. Only passing runs count: a failure can stop a suite early
// and flatter a mode.
func ComputeTestModeStats(runs []Run) TestModeStats {
	var passed []Run
	for _, r := range runs {
		if statExitedZero(r) {
			passed = append(passed, r)
		}
	}
	return TestModeStats{
		Sequential: modeStats(passed, TestModeSequential),
		Parallel:   modeStats(passed, TestModeParallel),
	}
}

// modeStats summarises the runs whose suites ran the way mode says.
func modeStats(runs []Run, mode TestMode) ModeStats {
	var runTimes, testTimes []*float64
	for _, r := range runs {
		own := TestModeOf(r)
		if own == nil || *own != mode {
			continue
		}
		runTimes = append(runTimes, statMsAsFloat(r.RunMs))
		testTimes = append(testTimes, statMsAsFloat(testsDuration(r)))
	}
	return ModeStats{
		Runs:  len(runTimes),
		Run:   Summarize(runTimes),
		Tests: Summarize(testTimes),
	}
}

// testsDuration is the duration of the run's first "Tests" group step.
func testsDuration(run Run) *int64 {
	for _, s := range run.Steps {
		if s.Name == "Tests" {
			return s.DurationMs
		}
	}
	return nil
}

// ComputeBottlenecks returns everything the bottleneck panel shows, over the
// newest limit finished runs.
func ComputeBottlenecks(runs []Run, limit int) Bottlenecks {
	recent := RecentFinished(runs, limit)
	passed := 0
	slotWaits := make([]*float64, 0, len(recent))
	runTimes := make([]*float64, 0, len(recent))
	totals := make([]*float64, 0, len(recent))
	for _, r := range recent {
		if statExitedZero(r) {
			passed++
		}
		slotWaits = append(slotWaits, statMsAsFloat(r.SlotWaitMs))
		runTimes = append(runTimes, statMsAsFloat(r.RunMs))
		totals = append(totals, statMsAsFloat(r.TotalMs))
	}
	return Bottlenecks{
		Runs:       len(recent),
		Passed:     passed,
		Steps:      StepStats(recent),
		SlotWait:   Summarize(slotWaits),
		Run:        Summarize(runTimes),
		Total:      Summarize(totals),
		ByTestMode: ComputeTestModeStats(recent),
		Slowest:    slowestRuns(recent),
	}
}

// slowestRuns returns the rows of the slowest-runs table, longest run first.
func slowestRuns(recent []Run) []SlowRun {
	byRunTime := append([]Run(nil), recent...)
	sort.SliceStable(byRunTime, func(a, b int) bool {
		return statMsOrZero(byRunTime[a].RunMs) > statMsOrZero(byRunTime[b].RunMs)
	})
	if len(byRunTime) > slowestRunsShown {
		byRunTime = byRunTime[:slowestRunsShown]
	}
	rows := make([]SlowRun, 0, len(byRunTime))
	for _, r := range byRunTime {
		rows = append(rows, SlowRun{
			ID:          r.ID,
			Name:        r.Name,
			Repo:        r.Repo,
			Branch:      r.Branch,
			FinishedAt:  r.FinishedAt,
			ExitCode:    r.ExitCode,
			RunMs:       r.RunMs,
			SlotWaitMs:  r.SlotWaitMs,
			SlowestStep: slowestLeafStep(r),
		})
	}
	return rows
}

func statExitedZero(run Run) bool {
	return run.ExitCode != nil && *run.ExitCode == 0
}

func statMsOrZero(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func statFloatOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func statMsAsFloat(v *int64) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}

func statFloatRefs(values []float64) []*float64 {
	ptrs := make([]*float64, len(values))
	for i := range values {
		ptrs[i] = &values[i]
	}
	return ptrs
}
