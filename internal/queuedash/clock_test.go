package queuedash

import (
	"strings"
	"testing"
	"time"
)

// useClockLocation sets the zone stamps are read in for one test.
func useClockLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %s: %v", name, err)
	}
	previous := clockLocation
	clockLocation = loc
	t.Cleanup(func() { clockLocation = previous })
	return loc
}

// Denver observes DST, so some cases cross the 25-hour day daylight saving
// time ends on as well as ordinary midnights.
func useDenver(t *testing.T) func(y int, mo time.Month, d, h, mi, s int) int64 {
	t.Helper()
	loc := useClockLocation(t, "America/Denver")
	return func(y int, mo time.Month, d, h, mi, s int) int64 {
		return time.Date(y, mo, d, h, mi, s, 0, loc).UnixMilli()
	}
}

func TestParseClockSplitsAStampIntoNumbers(t *testing.T) {
	got, ok := parseClock("09:05:07")
	if !ok || got != (clockParts{hours: 9, minutes: 5, seconds: 7}) {
		t.Errorf("parseClock(09:05:07) = %+v, %v", got, ok)
	}
}

func TestParseClockRejectsNon24HourStamps(t *testing.T) {
	for _, clock := range []string{"9:05:07", "24:00:00", "12:60:00", "noon"} {
		t.Run(clock, func(t *testing.T) {
			if got, ok := parseClock(clock); ok {
				t.Errorf("parseClock(%q) = %+v, want rejection", clock, got)
			}
		})
	}
}

func TestResolveClock(t *testing.T) {
	local := useDenver(t)
	// 2026-11-01 in Denver: 01:00-01:59 happens in MDT (UTC-6), then again
	// in MST (UTC-7).
	mdt := func(h, m int) int64 { return time.Date(2026, 11, 1, h+6, m, 0, 0, time.UTC).UnixMilli() }
	mst := func(h, m int) int64 { return time.Date(2026, 11, 1, h+7, m, 0, 0, time.UTC).UnixMilli() }

	cases := []struct {
		name    string
		resolve func(string, int64) int64
		clock   string
		anchor  int64
		want    int64
	}{
		{
			name: "after: places a stamp later the same day", resolve: ResolveClockAfter,
			clock: "15:42:17", anchor: local(2026, 10, 14, 15, 40, 0),
			want: local(2026, 10, 14, 15, 42, 17),
		},
		{
			// The log's birth time is sub-second; the slot stamp is truncated.
			name: "after: keeps a stamp a moment before the anchor", resolve: ResolveClockAfter,
			clock: "15:42:17", anchor: local(2026, 10, 14, 15, 42, 17) + 600,
			want: local(2026, 10, 14, 15, 42, 17),
		},
		{
			name: "after: moves a stamp past midnight onto the next day", resolve: ResolveClockAfter,
			clock: "00:00:05", anchor: local(2026, 10, 14, 23, 50, 0),
			want: local(2026, 10, 15, 0, 0, 5),
		},
		{
			name: "after: crosses the end of daylight saving time", resolve: ResolveClockAfter,
			clock: "00:15:00", anchor: local(2026, 10, 31, 23, 30, 0),
			want: local(2026, 11, 1, 0, 15, 0),
		},
		{
			name: "repeated hour: second 01:xx after a queue time in the first", resolve: ResolveClockAfter,
			clock: "01:05:00", anchor: mdt(1, 55), want: mst(1, 5),
		},
		{
			name: "repeated hour: keeps the first 01:xx when that is next", resolve: ResolveClockAfter,
			clock: "01:30:00", anchor: mdt(1, 10), want: mdt(1, 30),
		},
		{
			name: "repeated hour: works backward into the first 01:xx", resolve: ResolveClockBefore,
			clock: "01:55:00", anchor: mst(1, 10), want: mdt(1, 55),
		},
		{
			name: "before: places a stamp earlier the same day", resolve: ResolveClockBefore,
			clock: "15:42:17", anchor: local(2026, 10, 14, 15, 50, 0),
			want: local(2026, 10, 14, 15, 42, 17),
		},
		{
			name: "before: moves a stamp from before midnight onto the previous day", resolve: ResolveClockBefore,
			clock: "23:59:00", anchor: local(2026, 10, 15, 0, 10, 0),
			want: local(2026, 10, 14, 23, 59, 0),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.resolve(tc.clock, tc.anchor)
			if got != tc.want {
				t.Errorf("resolve(%q) = %s, want %s", tc.clock,
					time.UnixMilli(got).UTC(), time.UnixMilli(tc.want).UTC())
			}
		})
	}
}

func TestResolveClockFallsBackToTheGuessForASkippedTime(t *testing.T) {
	local := useDenver(t)
	// 2026-03-08 02:30 never happens in Denver; JavaScript reads it with the
	// offset before the jump (MST), which lands on 03:30 MDT.
	anchor := local(2026, 3, 8, 1, 0, 0)
	want := time.Date(2026, 3, 8, 9, 30, 0, 0, time.UTC).UnixMilli()
	if got := ResolveClockAfter("02:30:00", anchor); got != want {
		t.Errorf("ResolveClockAfter = %s, want %s", time.UnixMilli(got).UTC(), time.UnixMilli(want).UTC())
	}
}

func TestResolveClockBeforePanicsOnAMalformedStamp(t *testing.T) {
	defer func() {
		recovered := recover()
		message, ok := recovered.(string)
		if !ok || !strings.Contains(message, "not a clock time") {
			t.Errorf("recovered %v, want a 'not a clock time' panic", recovered)
		}
	}()
	ResolveClockBefore("7pm", time.Now().UnixMilli())
}
