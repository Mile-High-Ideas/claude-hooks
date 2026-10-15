package queuedash

import (
	"math"
	"testing"
)

func TestParseDurationMsReadsPrintedDurations(t *testing.T) {
	cases := []struct {
		text string
		want int64
	}{
		{"0ms", 0},
		{"524ms", 524},
		{"1.1s", 1100},
		{"12s", 12_000},
		{"6m48s", 408_000},
		{"12m53s", 773_000},
		{"2m", 120_000},
		{"1h2m3s", 3_723_000},
		{" 4.0s ", 4000},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := ParseDurationMs(tc.text)
			if got == nil {
				t.Fatalf("ParseDurationMs(%q) = nil, want %d", tc.text, tc.want)
			}
			if *got != tc.want {
				t.Errorf("ParseDurationMs(%q) = %d, want %d", tc.text, *got, tc.want)
			}
		})
	}
}

func TestParseDurationMsRejectsTextThatIsNotADuration(t *testing.T) {
	for _, text := range []string{"", "skipped", "12 s", "5s and more"} {
		t.Run(text, func(t *testing.T) {
			if got := ParseDurationMs(text); got != nil {
				t.Errorf("ParseDurationMs(%q) = %d, want nil", text, *got)
			}
		})
	}
}

func TestFormatDurationMs(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name string
		ms   *float64
		want string
	}{
		{"milliseconds", f(840), "840ms"},
		{"seconds", f(12_400), "12.4s"},
		{"minutes", f(408_000), "6m48s"},
		{"hours", f(3_900_000), "1h05m"},
		{"tie rounds up like toFixed", f(12_250), "12.3s"},
		{"null", nil, "-"},
		{"NaN", f(math.NaN()), "-"},
		{"infinity", f(math.Inf(1)), "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDurationMs(tc.ms); got != tc.want {
				t.Errorf("FormatDurationMs = %q, want %q", got, tc.want)
			}
		})
	}
}
