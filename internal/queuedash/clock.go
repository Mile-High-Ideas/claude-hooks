package queuedash

import (
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Queue logs stamp events with a local wall-clock time and no date
// ("14:40:18"). These helpers place a stamp on a real instant from one known
// instant in the same log: when it was queued (every stamp is at or after
// it) or when it was last written (every stamp is at or before it). A log
// spans well under a day, so the nearest matching instant on the right side
// of the anchor is the one, including for a run that crosses midnight.

// clockLocation is the zone the queue's stamps were written in. Tests swap
// it for a fixed zone.
var clockLocation = time.Local

// clockToleranceMs is the slack around the anchor: stamps are truncated to
// the second and the anchor can come from file metadata a moment off the
// logged event.
const clockToleranceMs = 5 * 60_000

const (
	hourMs   = 3_600_000
	daySecs  = 86_400
	msPerSec = 1_000
)

var clockPattern = regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2})$`)

// clockParts is a parsed "HH:MM:SS".
type clockParts struct {
	hours, minutes, seconds int
}

// parseClock splits "HH:MM:SS" into numbers, rejecting anything that is not
// a 24-hour stamp.
func parseClock(clock string) (clockParts, bool) {
	m := clockPattern.FindStringSubmatch(clock)
	if m == nil {
		return clockParts{}, false
	}
	hours, _ := strconv.Atoi(m[1])
	minutes, _ := strconv.Atoi(m[2])
	seconds, _ := strconv.Atoi(m[3])
	if hours > 23 || minutes > 59 || seconds > 59 {
		return clockParts{}, false
	}
	return clockParts{hours, minutes, seconds}, true
}

// ResolveClockAfter returns the first instant, in Unix milliseconds,
// showing clock that is not before anchorMs (less a small tolerance).
//
// It panics on a clock that is not "HH:MM:SS": callers pass only stamps the
// log patterns matched, and guessing an instant would be worse.
func ResolveClockAfter(clock string, anchorMs int64) int64 {
	candidates := nearbyOccurrences(clock, anchorMs)
	for _, t := range candidates {
		if t >= anchorMs-clockToleranceMs {
			return t
		}
	}
	return candidates[len(candidates)-1]
}

// ResolveClockBefore returns the last instant, in Unix milliseconds,
// showing clock that is not after anchorMs (plus a small tolerance).
//
// It panics on a clock that is not "HH:MM:SS", as ResolveClockAfter does.
func ResolveClockBefore(clock string, anchorMs int64) int64 {
	candidates := nearbyOccurrences(clock, anchorMs)
	for i := len(candidates) - 1; i >= 0; i-- {
		if candidates[i] <= anchorMs+clockToleranceMs {
			return candidates[i]
		}
	}
	return candidates[0]
}

// nearbyOccurrences returns the instants showing clock from the local day
// before to the local day after anchorMs, in order.
func nearbyOccurrences(clock string, anchorMs int64) []int64 {
	parts, ok := parseClock(clock)
	if !ok {
		panic("not a clock time: " + clock)
	}
	var all []int64
	for _, offset := range []int{-1, 0, 1} {
		all = append(all, occurrencesOnLocalDay(parts, anchorMs, offset)...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return all
}

// occurrencesOnLocalDay returns every instant showing this wall-clock time
// on the local day of dayMs shifted by dayOffset days. Usually one; two in
// the hour repeated when daylight saving time ends (01:05 MDT and 01:05
// MST), where a single date construction yields only one of them.
func occurrencesOnLocalDay(parts clockParts, dayMs int64, dayOffset int) []int64 {
	year, month, day := time.UnixMilli(dayMs).In(clockLocation).Date()
	guess := jsLocalDateMs(year, month, day+dayOffset, parts)
	_, _, guessDay := time.UnixMilli(guess).In(clockLocation).Date()
	var matches []int64
	for _, t := range []int64{guess - hourMs, guess, guess + hourMs} {
		if showsClock(t, guessDay, parts) {
			matches = append(matches, t)
		}
	}
	// A time skipped when daylight saving starts never appears in a log;
	// fall back to the guess rather than failing.
	if len(matches) == 0 {
		return []int64{guess}
	}
	return matches
}

// showsClock reports whether instant ms reads as this time of day on the
// given day of the month.
func showsClock(ms int64, dayOfMonth int, parts clockParts) bool {
	local := time.UnixMilli(ms).In(clockLocation)
	return local.Day() == dayOfMonth &&
		local.Hour() == parts.hours &&
		local.Minute() == parts.minutes &&
		local.Second() == parts.seconds
}

// jsLocalDateMs is JavaScript's new Date(y, m, d, h, mi, s).getTime() in
// clockLocation. Go's time.Date leaves the choice unspecified for a repeated
// or skipped wall time; JavaScript takes the earlier of a repeated time and
// reads a skipped one with the offset in force before the transition.
func jsLocalDateMs(year int, month time.Month, day int, parts clockParts) int64 {
	wall := time.Date(year, month, day, parts.hours, parts.minutes, parts.seconds, 0, time.UTC).Unix()
	before := offsetAt(wall - daySecs)
	var earliest *int64
	for _, offset := range []int64{before, offsetAt(wall), offsetAt(wall + daySecs)} {
		instant := wall - offset
		if offsetAt(instant) != offset {
			continue
		}
		if earliest == nil || instant < *earliest {
			earliest = &instant
		}
	}
	if earliest == nil {
		return (wall - before) * msPerSec
	}
	return *earliest * msPerSec
}

// offsetAt returns clockLocation's UTC offset in seconds at a Unix instant.
func offsetAt(unix int64) int64 {
	_, offset := time.Unix(unix, 0).In(clockLocation).Zone()
	return int64(offset)
}
