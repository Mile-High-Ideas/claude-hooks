package queuedash

import (
	"fmt"
	"strings"
)

// FormatSummary is the text `commit-queue dashboard --once` prints: queue
// counts, wait and slot-time percentiles, slot time by test mode, and the
// slowest steps.
func FormatSummary(state DashboardState, ingested int) string {
	b := state.Bottlenecks
	lines := []string{
		fmt.Sprintf("History: %d runs, %d steps (%d new this scan)", state.History.Runs, state.History.Steps, ingested),
		fmt.Sprintf("Queue now: %d running, %d waiting, %d abandoned", len(state.Running)+len(state.HeldWithoutRun), len(state.Waiting), len(state.Abandoned)),
		fmt.Sprintf("Last %d finished runs: %d passed", b.Runs, b.Passed),
		"  slot wait   " + p50p95(b.SlotWait),
		"  in slot     " + p50p95(b.Run),
		"  queue->done " + p50p95(b.Total),
	}
	lines = append(lines, testModeLines(b.ByTestMode)...)
	lines = append(lines, `Slowest steps by p95 (groups such as "Tests" excluded):`)
	shown := 0
	for _, s := range b.Steps {
		if s.Group || shown == 10 {
			continue
		}
		shown++
		lines = append(lines, fmt.Sprintf("  %-30s p50 %7s  p95 %7s  max %7s  (%d)",
			s.Name, FormatDurationMs(s.P50), FormatDurationMs(s.P95), FormatDurationMs(s.Max), s.Count))
	}
	return strings.Join(lines, "\n") + "\n"
}

func p50p95(s Summary) string {
	return fmt.Sprintf("p50 %s, p95 %s", FormatDurationMs(s.P50), FormatDurationMs(s.P95))
}

// testModeLines gives one line per test mode with passing runs in the
// window, so the effect of testConfig.parallel reads off the summary.
func testModeLines(m TestModeStats) []string {
	var lines []string
	for _, mode := range []struct {
		name  string
		stats ModeStats
	}{{"sequential", m.Sequential}, {"parallel", m.Parallel}} {
		if mode.stats.Runs == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("  in slot, %s tests (%d passing) %s; Tests phase p50 %s",
			mode.name, mode.stats.Runs, p50p95(mode.stats.Run), FormatDurationMs(mode.stats.Tests.P50)))
	}
	return lines
}
