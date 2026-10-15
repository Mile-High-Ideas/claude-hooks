package queuedash

import (
	"strconv"
	"strings"
)

// abandonedAfterMs applies to a log with no runner pid to check (logs from
// before the preamble existed): one that stopped growing this long ago, with
// no exit file and no slot, belongs to a runner that died rather than one
// between releasing its slot and writing its exit code.
const abandonedAfterMs = 2 * 60_000

// SetupStep is the derived step covering the convex-gen and OpenAPI work
// between taking a slot and the first stamped check.
const SetupStep = "Setup (convex-gen, OpenAPI)"

// FinishStep is the derived step covering commitlint and git after the last
// stamped check.
const FinishStep = "Finish (commitlint, git)"

// clockResolverFunc places an optional "HH:MM:SS" stamp on an instant.
type clockResolverFunc func(clock *string) *int64

// BuildRun builds a run from one log file and what sits next to it. It is
// pure: everything it needs is in the input.
func BuildRun(in RunInput) Run {
	parsed := ParseLog(in.Text)
	queuedAt := queuedAtOf(parsed, in)
	resolve := clockResolver(queuedAt, in)
	var slotAcquiredAt *int64
	if parsed.Slot != nil {
		slotAcquiredAt = resolve(&parsed.Slot.Clock)
	}
	var finishedAt *int64
	var exitCode *int
	if in.Exit != nil {
		finishedAt = int64Ptr(in.Exit.MtimeMs)
		code := in.Exit.Code
		exitCode = &code
	}
	steps := withDerivedSteps(absoluteSteps(parsed, resolve), slotAcquiredAt, finishedAt, exitCode)
	return Run{
		ID:             runID(in, firstSet(slotAcquiredAt, queuedAt, finishedAt, &in.LogMtimeMs)),
		Name:           in.Name,
		RepoKey:        in.RepoKey,
		Repo:           in.Repo,
		State:          stateOf(parsed, in),
		Branch:         firstString(parsed.Branch, parsed.ReportSlug),
		Worktree:       parsed.Worktree,
		Subject:        parsed.Subject,
		SHA:            commitSHA(parsed.Commit),
		QueuedAt:       queuedAt,
		Slot:           slotNumber(parsed.Slot),
		SlotAcquiredAt: slotAcquiredAt,
		FinishedAt:     finishedAt,
		ExitCode:       exitCode,
		SlotWaitMs:     span(queuedAt, slotAcquiredAt),
		RunMs:          span(slotAcquiredAt, finishedAt),
		TotalMs:        span(queuedAt, finishedAt),
		Failure:        failureOf(parsed, exitCode),
		Steps:          steps,
	}
}

// runID keys a run by its name and its first known instant, so a name
// queued again gets a new id. Runs from the Go queue carry their repo key,
// because names are unique only within a repo.
func runID(in RunInput, key int64) string {
	id := in.Name + "@" + strconv.FormatInt(key, 10)
	if in.RepoKey == "" {
		return id
	}
	return in.RepoKey + "/" + id
}

// clockResolver picks how stamps are placed on real instants: forward from
// the queue time when it is known, otherwise backward from the last write.
func clockResolver(queuedAt *int64, in RunInput) clockResolverFunc {
	if queuedAt != nil {
		anchor := *queuedAt
		return func(clock *string) *int64 {
			if clock == nil || *clock == "" {
				return nil
			}
			return int64Ptr(ResolveClockAfter(*clock, anchor))
		}
	}
	end := in.LogMtimeMs
	if in.Exit != nil {
		end = in.Exit.MtimeMs
	}
	return func(clock *string) *int64 {
		if clock == nil || *clock == "" {
			return nil
		}
		return int64Ptr(ResolveClockBefore(*clock, end))
	}
}

// queuedAtOf prefers the queue line the runner writes, then the log's
// creation time, which the runner also controls because it recreates the
// log on every queue. Some filesystems report no birth time, or the change
// time instead, so a birth after the last write is not trusted.
func queuedAtOf(parsed ParsedLog, in RunInput) *int64 {
	if parsed.QueuedAtMs != nil {
		return parsed.QueuedAtMs
	}
	birth := in.LogBirthMs
	if birth != nil && *birth > 0 && *birth <= in.LogMtimeMs {
		return int64Ptr(*birth)
	}
	return nil
}

// absoluteSteps places each parsed step on real instants. A step with no
// start line starts its printed duration before it ended, and the printed
// duration wins over the second-truncated stamps.
func absoluteSteps(parsed ParsedLog, resolve clockResolverFunc) []RunStep {
	steps := make([]RunStep, 0, len(parsed.Steps))
	for seq, step := range parsed.Steps {
		endedAt := resolve(step.EndClock)
		startedAt := resolve(step.StartClock)
		if startedAt == nil && endedAt != nil && step.DurationMs != nil {
			startedAt = int64Ptr(*endedAt - *step.DurationMs)
		}
		durationMs := step.DurationMs
		if durationMs == nil && startedAt != nil && endedAt != nil {
			durationMs = int64Ptr(*endedAt - *startedAt)
		}
		steps = append(steps, RunStep{
			Name:       step.Name,
			Seq:        seq,
			StartedAt:  startedAt,
			EndedAt:    endedAt,
			DurationMs: durationMs,
			Result:     step.Result,
			Note:       step.Note,
		})
	}
	return steps
}

// withDerivedSteps adds the stretches the runner does not stamp: setup
// between taking the slot and the first check, and finish after the last
// check. Each shows as running while it is the stretch in progress, so a
// live commit always has a current step.
func withDerivedSteps(steps []RunStep, slotAcquiredAt, finishedAt *int64, exitCode *int) []RunStep {
	all := make([]RunStep, 0, len(steps)+2)
	if setup := setupStep(steps, slotAcquiredAt, finishedAt, exitCode); setup != nil {
		all = append(all, *setup)
	}
	all = append(all, steps...)
	if finish := finishStep(steps, finishedAt, exitCode); finish != nil {
		all = append(all, *finish)
	}
	for seq := range all {
		all[seq].Seq = seq
	}
	return all
}

// setupStep is the stretch from taking the slot to the first check, or to
// the end of a commit that stopped before any check started (e.g.
// convex-gen failed).
func setupStep(steps []RunStep, slotAcquiredAt, finishedAt *int64, exitCode *int) *RunStep {
	if slotAcquiredAt == nil {
		return nil
	}
	if first, ok := earliestStart(steps); ok {
		return derivedStep(SetupStep, *slotAcquiredAt, &first, StepPassed)
	}
	if finishedAt == nil {
		return derivedStep(SetupStep, *slotAcquiredAt, nil, StepRunning)
	}
	result := StepFailed
	if exitCode != nil && *exitCode == 0 {
		result = StepPassed
	}
	return derivedStep(SetupStep, *slotAcquiredAt, finishedAt, result)
}

// finishStep is the stretch from the last check's end to the exit file.
// When every check passed but the commit failed, commitlint or git did.
func finishStep(steps []RunStep, finishedAt *int64, exitCode *int) *RunStep {
	last, ok := latestEnd(steps)
	if !ok || anyResult(steps, StepRunning) {
		return nil
	}
	if finishedAt == nil {
		return derivedStep(FinishStep, last, nil, StepRunning)
	}
	result := StepPassed
	if (exitCode == nil || *exitCode != 0) && !anyResult(steps, StepFailed) {
		result = StepFailed
	}
	return derivedStep(FinishStep, last, finishedAt, result)
}

// derivedStep is a stretch the runner does not stamp. An open one (nil
// endedAt) is still in progress.
func derivedStep(name string, startedAt int64, endedAt *int64, result StepResult) *RunStep {
	step := &RunStep{
		Name:      name,
		StartedAt: int64Ptr(startedAt),
		EndedAt:   endedAt,
		Result:    result,
		Derived:   true,
	}
	if endedAt != nil {
		step.DurationMs = int64Ptr(max(0, *endedAt-startedAt))
	}
	return step
}

// earliestStart is the first start time among the steps that have one.
func earliestStart(steps []RunStep) (int64, bool) {
	var earliest *int64
	for _, step := range steps {
		if step.StartedAt != nil && (earliest == nil || *step.StartedAt < *earliest) {
			earliest = step.StartedAt
		}
	}
	if earliest == nil {
		return 0, false
	}
	return *earliest, true
}

// latestEnd is the last end time among the steps that have one.
func latestEnd(steps []RunStep) (int64, bool) {
	var latest *int64
	for _, step := range steps {
		if step.EndedAt != nil && (latest == nil || *step.EndedAt > *latest) {
			latest = step.EndedAt
		}
	}
	if latest == nil {
		return 0, false
	}
	return *latest, true
}

// anyResult reports whether any step ended (or is) in this result.
func anyResult(steps []RunStep, result StepResult) bool {
	for _, step := range steps {
		if step.Result == result {
			return true
		}
	}
	return false
}

// stateOf decides where a run is in its life. A runner pid that is known
// dead wins over everything but an exit file; without a pid, a run holding
// no slot is judged by how long ago its log last grew.
func stateOf(parsed ParsedLog, in RunInput) RunState {
	if in.Exit != nil {
		return StateFinished
	}
	alive := runnerAlive(parsed.PID, in.IsRunnerAlive)
	if alive != nil && !*alive {
		return StateAbandoned
	}
	if parsed.Slot == nil {
		return StateWaiting
	}
	if in.SlotHeld || (alive != nil && *alive) {
		return StateRunning
	}
	if in.NowMs-in.LogMtimeMs > abandonedAfterMs {
		return StateAbandoned
	}
	return StateRunning
}

// runnerAlive asks whether the logged runner pid is alive, or returns nil
// when there is no pid (or no way) to ask.
func runnerAlive(pid *int, isRunnerAlive func(pid int) bool) *bool {
	if pid == nil || isRunnerAlive == nil {
		return nil
	}
	alive := isRunnerAlive(*pid)
	return &alive
}

// failureOf is one line on why a run failed, or nil when it passed or is
// not done.
func failureOf(parsed ParsedLog, exitCode *int) *string {
	if exitCode == nil || *exitCode == 0 {
		return nil
	}
	if parsed.HeaderTooLong {
		return stringPtr("commit header too long")
	}
	if failed, ok := innermostFailedStep(parsed.Steps); ok {
		if failed.Note != nil && *failed.Note != "" {
			return stringPtr(failed.Name + ": " + *failed.Note)
		}
		return stringPtr(failed.Name)
	}
	return parsed.LastLine
}

// innermostFailedStep names the inner step of a failed group: "Typecheck"
// fails because "Typecheck mobile" did, so the latter is the one to show.
func innermostFailedStep(steps []ParsedStep) (ParsedStep, bool) {
	for _, step := range steps {
		if step.Result != StepFailed {
			continue
		}
		if !hasFailedChild(steps, step.Name) {
			return step, true
		}
	}
	return ParsedStep{}, false
}

// hasFailedChild reports whether a failed step is named "<name> <more>".
func hasFailedChild(steps []ParsedStep, name string) bool {
	for _, other := range steps {
		if other.Result == StepFailed && strings.HasPrefix(other.Name, name+" ") {
			return true
		}
	}
	return false
}

// span is the non-negative time from one instant to another, or nil when
// either is unknown.
func span(from, to *int64) *int64 {
	if from == nil || to == nil {
		return nil
	}
	return int64Ptr(max(0, *to-*from))
}

// firstSet returns the first non-nil value; the last argument is never nil.
func firstSet(values ...*int64) int64 {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}

// firstString returns the first non-nil string.
func firstString(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// commitSHA is the commit's sha, or nil when no commit was made.
func commitSHA(commit *CommitLine) *string {
	if commit == nil {
		return nil
	}
	return stringPtr(commit.SHA)
}

// slotNumber is the held slot's number, or nil when no slot was taken.
func slotNumber(slot *SlotLine) *int {
	if slot == nil {
		return nil
	}
	number := slot.Number
	return &number
}

// int64Ptr returns a pointer to a copy of v.
func int64Ptr(v int64) *int64 {
	return &v
}
