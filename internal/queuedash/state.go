package queuedash

import "sort"

// PendingSteps returns the step names a running commit has not reached yet,
// in the order the newest run in history with real steps ran them. The suite
// picks checks by changed files, so this is an expectation, not a promise.
// history is newest first.
func PendingSteps(run Run, history []Run) []string {
	reference, ok := newestWithRealSteps(history)
	if !ok {
		return []string{}
	}
	seen := make(map[string]bool, len(run.Steps))
	for _, s := range run.Steps {
		seen[s.Name] = true
	}
	pending := []string{}
	for _, s := range reference.Steps {
		if !s.Derived && !seen[s.Name] {
			pending = append(pending, s.Name)
		}
	}
	return pending
}

// newestWithRealSteps finds the first run with a step that is not derived.
func newestWithRealSteps(history []Run) (Run, bool) {
	for _, h := range history {
		for _, s := range h.Steps {
			if !s.Derived {
				return h, true
			}
		}
	}
	return Run{}, false
}

// TypicalTimes returns the p50 per step name over the finished runs in
// history whose suites ran the way mode says, so a parallel run is not
// measured against sequential ones. It falls back to every run when mode is
// nil or no run matches.
func TypicalTimes(history []Run, mode *TestMode) map[string]*float64 {
	source := runsInMode(history, mode)
	if len(source) == 0 {
		source = history
	}
	finished := RecentFinished(source, len(source))
	typical := map[string]*float64{}
	for _, s := range StepStats(finished) {
		typical[s.Name] = s.P50
	}
	return typical
}

// runsInMode returns the runs whose own suites show mode; none for nil.
func runsInMode(history []Run, mode *TestMode) []Run {
	if mode == nil {
		return nil
	}
	var matching []Run
	for _, r := range history {
		if own := TestModeOf(r); own != nil && *own == *mode {
			matching = append(matching, r)
		}
	}
	return matching
}

// ExpectedTestMode returns how a live run's suites will run: its own
// evidence once two suites have started, otherwise however the newest run in
// history that shows a mode ran them, since that is how the hook is
// configured now. history is newest first.
func ExpectedTestMode(run Run, history []Run) *TestMode {
	if own := TestModeOf(run); own != nil {
		return own
	}
	for _, past := range history {
		if mode := TestModeOf(past); mode != nil {
			return mode
		}
	}
	return nil
}

// typicalCache computes TypicalTimes once per mode for one state build.
type typicalCache struct {
	history []Run
	byMode  map[TestMode]map[string]*float64
}

// forMode returns the typical times for mode; nil is cached under "".
func (c *typicalCache) forMode(mode *TestMode) map[string]*float64 {
	var key TestMode
	if mode != nil {
		key = *mode
	}
	if typical, ok := c.byMode[key]; ok {
		return typical
	}
	typical := TypicalTimes(c.history, mode)
	c.byMode[key] = typical
	return typical
}

// liveStep adds elapsed and typical times to a step. A running step's
// elapsed time is measured to now; a finished one's is its duration.
func liveStep(step RunStep, nowMs int64, typical map[string]*float64) LiveStep {
	elapsed := step.DurationMs
	if step.Result == StepRunning && step.StartedAt != nil {
		e := max(0, nowMs-*step.StartedAt)
		elapsed = &e
	}
	return LiveStep{RunStep: step, ElapsedMs: elapsed, TypicalMs: typical[step.Name]}
}

// runningView builds a running or abandoned run's view. TestMode is only
// what the run itself has shown, because each worktree reads
// testConfig.parallel from its own branch: the newest finished run says
// nothing certain about this one. The guess is still the best basis for
// typical times, so those use ExpectedTestMode.
func runningView(run Run, nowMs int64, history []Run, cache *typicalCache) RunningView {
	typical := cache.forMode(ExpectedTestMode(run, history))
	var elapsed *int64
	if run.SlotAcquiredAt != nil {
		e := nowMs - *run.SlotAcquiredAt
		elapsed = &e
	}
	steps := make([]LiveStep, 0, len(run.Steps))
	for _, s := range run.Steps {
		steps = append(steps, liveStep(s, nowMs, typical))
	}
	pendingNames := PendingSteps(run, history)
	pending := make([]PendingStep, 0, len(pendingNames))
	for _, name := range pendingNames {
		pending = append(pending, PendingStep{Name: name, TypicalMs: typical[name]})
	}
	return RunningView{
		Run:       run,
		TestMode:  TestModeOf(run),
		ElapsedMs: elapsed,
		Steps:     steps,
		Pending:   pending,
	}
}

// runViewsInState returns the views of runs in state, earliest slot first.
func runViewsInState(in StateInput, state RunState, cache *typicalCache) []RunningView {
	var matching []Run
	for _, r := range in.Runs {
		if r.State == state {
			matching = append(matching, r)
		}
	}
	sort.SliceStable(matching, func(a, b int) bool {
		return statMsOrZero(matching[a].SlotAcquiredAt) < statMsOrZero(matching[b].SlotAcquiredAt)
	})
	views := make([]RunningView, 0, len(matching))
	for _, r := range matching {
		views = append(views, runningView(r, in.NowMs, in.WindowRuns, cache))
	}
	return views
}

// waitingViews lists the queue's tickets in serving order. The ticket is the
// truth about who waits; a waiting run log with the same name and repo, if
// the waiter wrote one, adds the branch, subject and worktree.
func waitingViews(in StateInput) []WaitingView {
	views := make([]WaitingView, 0, len(in.Tickets))
	for i, t := range in.Tickets {
		view := WaitingView{
			Position: i + 1,
			Name:     t.Name,
			RepoKey:  t.RepoKey,
			Repo:     t.Repo,
			QueuedAt: t.QueuedAt,
			WaitMs:   max(0, in.NowMs-t.QueuedAt),
		}
		if r, ok := waitingRunFor(in.Runs, t); ok {
			view.Branch = r.Branch
			view.Subject = r.Subject
			view.Worktree = r.Worktree
		}
		views = append(views, view)
	}
	return views
}

// waitingRunFor finds the waiting run log that belongs to a ticket.
func waitingRunFor(runs []Run, ticket QueueTicket) (Run, bool) {
	for _, r := range runs {
		if r.State == StateWaiting && r.Name == ticket.Name && r.RepoKey == ticket.RepoKey {
			return r, true
		}
	}
	return Run{}, false
}

// heldWithoutRun returns the slots no running run accounts for: those taken
// by `commit-queue acquire`, which writes no run log.
func heldWithoutRun(slots []QueueSlot, running []RunningView) []QueueSlot {
	held := []QueueSlot{}
	for _, slot := range slots {
		if !slotHasRun(slot, running) {
			held = append(held, slot)
		}
	}
	return held
}

func slotHasRun(slot QueueSlot, running []RunningView) bool {
	for _, r := range running {
		if r.Name == slot.Owner && r.RepoKey == slot.RepoKey {
			return true
		}
	}
	return false
}

// recentViews labels each recent run with its own test mode.
func recentViews(recent []Run) []RecentView {
	views := make([]RecentView, 0, len(recent))
	for _, r := range recent {
		if r.Steps == nil {
			r.Steps = []RunStep{}
		}
		views = append(views, RecentView{Run: r, TestMode: TestModeOf(r)})
	}
	return views
}

// BuildDashboardState assembles the JSON the dashboard page polls. Waiting
// comes from the queue's tickets; running, abandoned and recent from run
// logs; the statistics from in.WindowRuns (newest first), covered in full.
// Every list is non-nil so the page reads [] rather than null.
func BuildDashboardState(in StateInput) DashboardState {
	cache := &typicalCache{history: in.WindowRuns, byMode: map[TestMode]map[string]*float64{}}
	slots := in.Slots
	if slots == nil {
		slots = []QueueSlot{}
	}
	running := runViewsInState(in, StateRunning, cache)
	return DashboardState{
		GeneratedAt:    in.NowMs,
		SlotCount:      in.SlotCount,
		Slots:          slots,
		Running:        running,
		HeldWithoutRun: heldWithoutRun(slots, running),
		Abandoned:      runViewsInState(in, StateAbandoned, cache),
		Waiting:        waitingViews(in),
		Recent:         recentViews(in.Recent),
		Bottlenecks:    ComputeBottlenecks(in.WindowRuns, len(in.WindowRuns)),
		History:        in.HistoryCounts,
	}
}
