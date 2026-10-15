package queuedash

import (
	"sort"
	"strings"
)

// suitePrefix starts the name of every per-app test suite step.
const suitePrefix = "Tests "

// TestModeOf tells whether a run's per-app test suites ran one after another
// or at the same time. The pre-commit hook gained testConfig.parallel without
// a log format change, so the only evidence is whether the "Tests <app>"
// steps overlap.
//
// A run with fewer than two timed suites carries no evidence either way and
// gets nil, as does a live run whose second suite has not started yet. Stamps
// are whole seconds, so a suite that starts in the second the previous one
// ended is sequential.
func TestModeOf(run Run) *TestMode {
	suites := timedSuites(run.Steps)
	if len(suites) < 2 {
		return nil
	}
	for i := 1; i < len(suites); i++ {
		if suitesOverlap(suites[i-1], suites[i]) {
			return testModePtr(TestModeParallel)
		}
	}
	return testModePtr(TestModeSequential)
}

// timedSuites returns the stamped, non-skipped suite steps by start time.
func timedSuites(steps []RunStep) []RunStep {
	var suites []RunStep
	for _, s := range steps {
		if s.Derived || !strings.HasPrefix(s.Name, suitePrefix) {
			continue
		}
		if s.Result == StepSkipped || s.StartedAt == nil {
			continue
		}
		suites = append(suites, s)
	}
	sort.SliceStable(suites, func(a, b int) bool {
		return *suites[a].StartedAt < *suites[b].StartedAt
	})
	return suites
}

// suitesOverlap reports whether next started before previous ended. A suite
// still running has no end, so anything started after it overlaps it.
func suitesOverlap(previous, next RunStep) bool {
	if previous.EndedAt == nil {
		return true
	}
	return *next.StartedAt < *previous.EndedAt
}

func testModePtr(mode TestMode) *TestMode {
	return &mode
}
