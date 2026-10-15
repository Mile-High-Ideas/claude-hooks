package queuedash

import (
	"regexp"
	"strconv"
	"strings"
)

// ParseLog's input is one run log written by the commit queue. Lines it
// understands, in the order they appear:
//
//	[run-commit] queued at 1760478120 pid 4242 on feat/x in /path/to/worktree
//	[run-commit] subject: feat(x): do the thing
//	[run-commit] commit header is 101 characters; commitlint allows 100:
//	[commit-queue] issue-984 waiting, position 2 of the line
//	[commit-slot] issue-984 holds slot-1 from 14:40:14
//	[commit-slot] reclaimed stale slot-2 from tours-1
//	  ▶ 14:40:18 Changelog
//	  ✅ Formatting (14:40:19, 1.1s)
//	  ❌ Formatting (14:18:52, 524ms, lint-staged failed)
//	  ⚠️  SRP compliance (15:42:28, 6.2s, 453 warnings, 4589 files) (warning)
//	📁 Reports will be written to: analysis-reports/<branch-slug>_<stamp>
//	[feat/926-video-blur 07cf8f799] feat(videos): add tours
//	[commit-slot] tours-1 released slot-2
//
// The first two run-commit lines exist only in newer logs; older logs parse
// without them. The commit-queue waiting line is informational and, like
// every line no pattern claims, is ignored apart from becoming LastLine.

// The patterns below are the JavaScript originals with JavaScript's
// character classes spelled out, because Go's \s, \S and . are narrower or
// wider than JavaScript's: \s there includes NBSP, BOM and Unicode spaces,
// and . excludes \r and the Unicode line separators.
const (
	jsSpaceChars = `\t\n\v\f\r \x{a0}\x{feff}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
	jsS          = `[` + jsSpaceChars + `]`
	jsNotS       = `[^` + jsSpaceChars + `]`
	jsDot        = `[^\n\r\x{2028}\x{2029}]`
)

// jsPattern compiles a pattern written with \s, \S and . meaning what they
// mean in JavaScript.
func jsPattern(pattern string) *regexp.Regexp {
	replacer := strings.NewReplacer(`\s`, jsS, `\S`, jsNotS, `.`, jsDot, `\.`, `\.`)
	return regexp.MustCompile(replacer.Replace(pattern))
}

var (
	queuedLine        = jsPattern(`^\[run-commit\] queued at (\d+) pid (\d+) on (\S+) in (.+)$`)
	subjectLine       = jsPattern(`^\[run-commit\] subject: (.*)$`)
	headerTooLongLine = jsPattern(`^\[run-commit\] commit header is \d+ characters`)
	slotHeldLine      = jsPattern(`^\[commit-slot\] (\S+) holds slot-(\d+) from (\d{2}:\d{2}:\d{2})$`)
	slotReleasedLine  = jsPattern(`^\[commit-slot\] (\S+) released slot-(\d+)$`)
	stepStartLine     = jsPattern(`^\s*▶ (\d{2}:\d{2}:\d{2}) (.+?)\s*$`)
	// The warning icon may or may not carry its emoji variation selector.
	stepEndLine   = jsPattern(`^\s*(✅|❌|⚠\x{fe0f}?)\s+(.+?) \((\d{2}:\d{2}:\d{2}), ([^,)]+)(?:, ([^)]*))?\)(?:\s*\((\w+)\))?\s*$`)
	reportDirLine = jsPattern(`Reports will be written to: analysis-reports/(.+)_\d{8}_\d{6}/?$`)
	commitLine    = jsPattern(`^\[(\S+) ([0-9a-f]{7,40})\] (.*)$`)
)

// isJSSpace reports whether r is whitespace to JavaScript's String.trim.
func isJSSpace(r rune) bool {
	switch {
	case r == '\t', r == '\n', r == '\v', r == '\f', r == '\r', r == ' ':
		return true
	case r == 0xa0, r == 0xfeff, r == 0x1680, r == 0x2028, r == 0x2029:
		return true
	case r >= 0x2000 && r <= 0x200a:
		return true
	case r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// jsTrim is JavaScript's String.prototype.trim. strings.TrimSpace differs
// at the edges: it trims U+0085 and keeps U+FEFF.
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// ParseLog parses a whole or partial run log. Clock stamps stay "HH:MM:SS"
// strings; BuildRun places them on real instants.
func ParseLog(text string) ParsedLog {
	log := ParsedLog{Steps: []ParsedStep{}}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if jsTrim(line) == "" {
			continue
		}
		applyLine(&log, line)
		if !slotReleasedLine.MatchString(line) {
			last := jsTrim(line)
			log.LastLine = &last
		}
	}
	if log.Commit != nil {
		branch := log.Commit.Branch
		log.Branch = &branch
		if log.Subject == nil {
			subject := log.Commit.Subject
			log.Subject = &subject
		}
	}
	return log
}

// applyLine applies one line to the log being built; unrecognised lines
// are ignored.
func applyLine(log *ParsedLog, line string) {
	if m := queuedLine.FindStringSubmatch(line); m != nil {
		applyQueued(log, m)
		return
	}
	if m := subjectLine.FindStringSubmatch(line); m != nil {
		log.Subject = stringPtr(m[1])
		return
	}
	if headerTooLongLine.MatchString(line) {
		log.HeaderTooLong = true
		return
	}
	if m := slotHeldLine.FindStringSubmatch(line); m != nil {
		applySlotHeld(log, m)
		return
	}
	if slotReleasedLine.MatchString(line) {
		log.Released = true
		return
	}
	if m := stepStartLine.FindStringSubmatch(line); m != nil {
		startStep(log, m)
		return
	}
	if m := stepEndLine.FindStringSubmatch(line); m != nil {
		endStep(log, m)
		return
	}
	if m := reportDirLine.FindStringSubmatch(line); m != nil {
		log.ReportSlug = stringPtr(m[1])
		return
	}
	if m := commitLine.FindStringSubmatch(line); m != nil {
		log.Commit = &CommitLine{Branch: m[1], SHA: m[2], Subject: m[3]}
	}
}

// applyQueued records "[run-commit] queued at <unix> pid <pid> on <branch>
// in <worktree>".
func applyQueued(log *ParsedLog, m []string) {
	if seconds, err := strconv.ParseInt(m[1], 10, 64); err == nil {
		queuedAtMs := seconds * 1000
		log.QueuedAtMs = &queuedAtMs
	}
	if pid, err := strconv.Atoi(m[2]); err == nil {
		log.PID = &pid
	}
	log.Branch = stringPtr(m[3])
	log.Worktree = stringPtr(m[4])
}

// applySlotHeld records "[commit-slot] <name> holds slot-<n> from HH:MM:SS".
func applySlotHeld(log *ParsedLog, m []string) {
	number, err := strconv.Atoi(m[2])
	if err != nil {
		return
	}
	log.Slot = &SlotLine{Number: number, Clock: m[3]}
}

// startStep records "▶ HH:MM:SS Name" as a running step.
func startStep(log *ParsedLog, m []string) {
	log.Steps = append(log.Steps, ParsedStep{
		Name:       m[2],
		StartClock: stringPtr(m[1]),
		Result:     StepRunning,
	})
}

// endStep closes the earliest still-running step with this name, or
// records a step that finished without a start line.
func endStep(log *ParsedLog, m []string) {
	icon, name, endClock, duration, note, suffix := m[1], m[2], m[3], m[4], m[5], m[6]
	finished := ParsedStep{
		Name:       name,
		EndClock:   stringPtr(endClock),
		DurationMs: ParseDurationMs(duration),
		Result:     stepResult(icon, note),
		Note:       joinNotes(note, suffix),
	}
	for i := range log.Steps {
		open := &log.Steps[i]
		if open.Name != name || open.Result != StepRunning {
			continue
		}
		finished.StartClock = open.StartClock
		*open = finished
		return
	}
	log.Steps = append(log.Steps, finished)
}

// stepResult maps a result icon and the runner's note to a step result.
// Only the note inside the parentheses says "skipped"; a trailing
// "(skipped)" suffix does not.
func stepResult(icon, note string) StepResult {
	if icon == "❌" {
		return StepFailed
	}
	if strings.HasPrefix(icon, "⚠") {
		return StepWarning
	}
	if note == "skipped" {
		return StepSkipped
	}
	return StepPassed
}

// joinNotes joins the non-empty notes with ", ", or returns nil when there
// are none.
func joinNotes(notes ...string) *string {
	kept := make([]string, 0, len(notes))
	for _, note := range notes {
		if note != "" {
			kept = append(kept, note)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return stringPtr(strings.Join(kept, ", "))
}

// stringPtr returns a pointer to a copy of s.
func stringPtr(s string) *string {
	return &s
}
