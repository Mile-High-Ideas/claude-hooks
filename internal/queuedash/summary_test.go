package queuedash

import (
	"regexp"
	"strings"
	"testing"
)

func dashF64(v float64) *float64 { return &v }

func TestFormatSummary(t *testing.T) {
	minute := Summary{Count: 1, P50: dashF64(60_000), P95: dashF64(120_000), Max: dashF64(120_000)}
	state := DashboardState{
		History:        HistoryCounts{Runs: 3, Steps: 30},
		Running:        []RunningView{{}},
		HeldWithoutRun: []QueueSlot{{}},
		Waiting:        []WaitingView{{}, {}},
		Bottlenecks: Bottlenecks{
			Runs: 3, Passed: 2, SlotWait: minute, Run: minute, Total: minute,
			ByTestMode: TestModeStats{
				Sequential: ModeStats{Runs: 2, Run: minute, Tests: Summary{P50: dashF64(380_000)}},
			},
			Steps: []StepStat{
				{Name: "Tests", Group: true, Summary: Summary{Count: 3, P50: dashF64(9), P95: dashF64(9), Max: dashF64(9)}},
				{Name: "Tests web", Summary: Summary{Count: 3, P50: dashF64(340_000), P95: dashF64(675_000), Max: dashF64(867_000)}},
			},
		},
	}
	text := FormatSummary(state, 3)
	for _, want := range []string{
		"History: 3 runs, 30 steps (3 new this scan)",
		// A slot held by acquire counts as running.
		"2 running, 2 waiting, 0 abandoned",
		"slot wait   p50 1m00s, p95 2m00s",
		"in slot, sequential tests (2 passing) p50 1m00s, p95 2m00s; Tests phase p50 6m20s",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
	if !regexp.MustCompile(`Tests web\s+p50\s+5m40s\s+p95\s+11m15s\s+max\s+14m27s\s+\(3\)`).MatchString(text) {
		t.Errorf("summary lacks the Tests web row:\n%s", text)
	}
	if regexp.MustCompile(`(?m)^\s+Tests\s+p50`).MatchString(text) {
		t.Errorf("summary lists the Tests group:\n%s", text)
	}
	if strings.Contains(text, "parallel tests") {
		t.Errorf("summary lists a mode with no runs:\n%s", text)
	}
}
