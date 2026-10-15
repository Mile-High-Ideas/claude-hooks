package queuedash

import "testing"

func qdI64(v int64) *int64        { return &v }
func qdF64(v float64) *float64    { return &v }
func qdInt(v int) *int            { return &v }
func qdStr(v string) *string      { return &v }
func qdMode(v TestMode) *TestMode { return &v }

// modeString renders a *TestMode for failure messages.
func modeString(m *TestMode) string {
	if m == nil {
		return "<nil>"
	}
	return string(*m)
}

// timedStep is a step with explicit stamps; a nil end and the "running"
// result describe a suite still going.
type timedStep struct {
	name   string
	start  *int64
	end    *int64
	result StepResult
}

func runWithTimedSteps(steps []timedStep) Run {
	run := Run{Steps: []RunStep{}}
	for seq, s := range steps {
		result := s.result
		if result == "" {
			result = StepPassed
		}
		var duration *int64
		if s.start != nil && s.end != nil {
			duration = qdI64(*s.end - *s.start)
		}
		run.Steps = append(run.Steps, RunStep{
			Name: s.name, Seq: seq, StartedAt: s.start, EndedAt: s.end,
			DurationMs: duration, Result: result,
		})
	}
	return run
}

func TestTestModeOf(t *testing.T) {
	tests := []struct {
		name  string
		steps []timedStep
		want  *TestMode
	}{
		{
			name: "calls suites that overlap parallel",
			steps: []timedStep{
				{name: "Tests web", start: qdI64(0), end: qdI64(240_000)},
				{name: "Tests backend", start: qdI64(0), end: qdI64(135_000)},
				{name: "Tests mobile", start: qdI64(1000), end: qdI64(3000)},
			},
			want: qdMode(TestModeParallel),
		},
		{
			name: "calls back-to-back suites sequential, including a start in the end second",
			steps: []timedStep{
				{name: "Tests web", start: qdI64(0), end: qdI64(240_000)},
				{name: "Tests mobile", start: qdI64(240_000), end: qdI64(242_000)},
				{name: "Tests backend", start: qdI64(242_000), end: qdI64(377_000)},
			},
			want: qdMode(TestModeSequential),
		},
		{
			name: "calls a live run parallel once two suites are running at once",
			steps: []timedStep{
				{name: "Tests web", start: qdI64(0), result: StepRunning},
				{name: "Tests backend", start: qdI64(0), result: StepRunning},
			},
			want: qdMode(TestModeParallel),
		},
		{
			name:  "has no answer while only one suite has started",
			steps: []timedStep{{name: "Tests web", start: qdI64(0), result: StepRunning}},
			want:  nil,
		},
		{
			name: "ignores the Tests group, skipped suites and other steps",
			steps: []timedStep{
				{name: "Tests", start: qdI64(0), end: qdI64(400_000)},
				{name: "Tests web", start: qdI64(0), end: qdI64(240_000)},
				{name: "Lint web", start: qdI64(0), end: qdI64(10_000)},
				{name: "Tests mobile", start: qdI64(5000), end: qdI64(5000), result: StepSkipped},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TestModeOf(runWithTimedSteps(tt.steps))
			if modeString(got) != modeString(tt.want) {
				t.Errorf("TestModeOf = %s, want %s", modeString(got), modeString(tt.want))
			}
		})
	}
}
