package queuedash

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
)

// Duration strings as the pre-commit runner prints them: "0ms", "524ms",
// "1.1s", "12s", "6m48s", "12m53s", and in principle "1h2m".

// durationPart matches one number-and-unit at the start of the rest of the
// text. "ms" comes before "m" so "524ms" is not read as minutes.
var durationPart = regexp.MustCompile(`^(\d+(?:\.\d+)?)(ms|h|m|s)`)

var durationUnitMs = map[string]float64{
	"h":  3_600_000,
	"m":  60_000,
	"s":  1_000,
	"ms": 1,
}

// ParseDurationMs converts a printed duration to whole milliseconds. It
// returns nil when the text, once trimmed, is not entirely a duration.
func ParseDurationMs(text string) *int64 {
	rest := jsTrim(text)
	if rest == "" {
		return nil
	}
	total := 0.0
	for rest != "" {
		match := durationPart.FindStringSubmatch(rest)
		if match == nil {
			return nil
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return nil
		}
		total += value * durationUnitMs[match[2]]
		rest = rest[len(match[0]):]
	}
	ms := int64(jsRound(total))
	return &ms
}

// FormatDurationMs formats milliseconds the way a person reads a stopwatch:
// 840ms, 12.4s, 6m48s, 1h05m. A missing or non-finite value prints as "-".
func FormatDurationMs(ms *float64) string {
	if ms == nil || math.IsNaN(*ms) || math.IsInf(*ms, 0) {
		return "-"
	}
	value := *ms
	if value < 1_000 {
		return fmt.Sprintf("%dms", int64(jsRound(value)))
	}
	if value < 60_000 {
		return toFixed1(value/1_000) + "s"
	}
	totalSeconds := int64(jsRound(value / 1_000))
	hours := totalSeconds / 3_600
	minutes := (totalSeconds % 3_600) / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm", hours, minutes)
	}
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

// jsRound is JavaScript's Math.round: nearest integer, halves toward +∞.
func jsRound(x float64) float64 {
	return math.Floor(x + 0.5)
}

// toFixed1 is JavaScript's Number.prototype.toFixed(1) for a positive x. It
// rounds the exact binary value and breaks a tie toward the larger result,
// where strconv would round half to even (12.25 → "12.3", not "12.2").
func toFixed1(x float64) string {
	tenths := new(big.Float).SetPrec(128).SetFloat64(x)
	tenths.Mul(tenths, big.NewFloat(10))
	tenths.Add(tenths, big.NewFloat(0.5))
	n, _ := tenths.Int(nil)
	whole, frac := new(big.Int).QuoRem(n, big.NewInt(10), new(big.Int))
	return whole.String() + "." + frac.String()
}
