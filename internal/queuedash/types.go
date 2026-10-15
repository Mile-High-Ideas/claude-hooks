// Package queuedash turns commit-queue state and run logs into the commit
// queue dashboard: runs with absolute step timings, bottleneck statistics,
// and the JSON the dashboard page polls.
//
// It is a port of projectparabellum.com's scripts/agent-commit/dashboard
// (JavaScript). JSON field names match that version, so the page reads
// either. Times are Unix milliseconds; nil marshals as JSON null.
package queuedash

// StepResult is how a step ended, or "running".
type StepResult string

// Step results, as the pre-commit runner's icons map onto them.
const (
	StepRunning StepResult = "running"
	StepPassed  StepResult = "passed"
	StepFailed  StepResult = "failed"
	StepWarning StepResult = "warning"
	StepSkipped StepResult = "skipped"
)

// RunState is where a run is in its life.
type RunState string

// Run states.
const (
	StateWaiting   RunState = "waiting"
	StateRunning   RunState = "running"
	StateFinished  RunState = "finished"
	StateAbandoned RunState = "abandoned"
)

// TestMode is how a run's per-app test suites ran: one after another, or
// at the same time (claude-hooks testConfig.parallel).
type TestMode string

// Test modes.
const (
	TestModeSequential TestMode = "sequential"
	TestModeParallel   TestMode = "parallel"
)

// ParsedStep is one check as the log reports it, with clock stamps left as
// "HH:MM:SS" strings.
type ParsedStep struct {
	Name       string
	StartClock *string
	EndClock   *string
	DurationMs *int64
	Result     StepResult
	Note       *string
}

// SlotLine is the "[commit-slot] <name> holds slot-<n> from HH:MM:SS" line.
type SlotLine struct {
	Number int
	Clock  string
}

// CommitLine is git's "[<branch> <sha>] <subject>" line.
type CommitLine struct {
	Branch  string
	SHA     string
	Subject string
}

// ParsedLog is everything a run log says, before times are resolved.
type ParsedLog struct {
	QueuedAtMs    *int64
	PID           *int
	Branch        *string
	ReportSlug    *string
	Worktree      *string
	Subject       *string
	HeaderTooLong bool
	Slot          *SlotLine
	Released      bool
	Steps         []ParsedStep
	Commit        *CommitLine
	LastLine      *string
}

// RunStep is one step of a run with absolute times.
type RunStep struct {
	Name       string     `json:"name"`
	Seq        int        `json:"seq"`
	StartedAt  *int64     `json:"startedAt"`
	EndedAt    *int64     `json:"endedAt"`
	DurationMs *int64     `json:"durationMs"`
	Result     StepResult `json:"result"`
	Note       *string    `json:"note"`
	// Derived marks a stretch the runner does not stamp: setup before the
	// first check, and commitlint and git after the last.
	Derived bool `json:"derived"`
}

// Run is one queued commit.
type Run struct {
	ID string `json:"id"`
	// Name is the queue name the caller chose, unique within its repo.
	Name string `json:"name"`
	// RepoKey and Repo identify the repository (commitqueue.RepoKey and
	// RepoLabel). Empty for logs from the per-repo shell queue.
	RepoKey        string    `json:"repoKey"`
	Repo           string    `json:"repo"`
	State          RunState  `json:"state"`
	Branch         *string   `json:"branch"`
	Worktree       *string   `json:"worktree"`
	Subject        *string   `json:"subject"`
	SHA            *string   `json:"sha"`
	QueuedAt       *int64    `json:"queuedAt"`
	Slot           *int      `json:"slot"`
	SlotAcquiredAt *int64    `json:"slotAcquiredAt"`
	FinishedAt     *int64    `json:"finishedAt"`
	ExitCode       *int      `json:"exitCode"`
	SlotWaitMs     *int64    `json:"slotWaitMs"`
	RunMs          *int64    `json:"runMs"`
	TotalMs        *int64    `json:"totalMs"`
	Failure        *string   `json:"failure"`
	Steps          []RunStep `json:"steps"`
}

// ExitInfo is a run's exit file: its code and when it was written.
type ExitInfo struct {
	Code    int
	MtimeMs int64
}

// RunInput is everything BuildRun needs about one log.
type RunInput struct {
	Name    string
	RepoKey string
	Repo    string
	Text    string
	// LogBirthMs is when the log file was created, if the filesystem says.
	LogBirthMs *int64
	LogMtimeMs int64
	Exit       *ExitInfo
	// SlotHeld is whether the queue shows this run holding a slot now.
	SlotHeld      bool
	NowMs         int64
	IsRunnerAlive func(pid int) bool
}

// Summary is a distribution of durations in milliseconds.
type Summary struct {
	Count int      `json:"count"`
	P50   *float64 `json:"p50"`
	P95   *float64 `json:"p95"`
	Max   *float64 `json:"max"`
}

// StepStat is the distribution of one step's duration across runs.
type StepStat struct {
	Name    string `json:"name"`
	Derived bool   `json:"derived"`
	// Group is true for a step that contains others, such as "Tests"
	// around "Tests web".
	Group bool `json:"group"`
	Summary
}

// NamedDuration is a step name and how long it took.
type NamedDuration struct {
	Name       string `json:"name"`
	DurationMs int64  `json:"durationMs"`
}

// SlowRun is one row of the slowest-runs table.
type SlowRun struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Repo        string         `json:"repo"`
	Branch      *string        `json:"branch"`
	FinishedAt  *int64         `json:"finishedAt"`
	ExitCode    *int           `json:"exitCode"`
	RunMs       *int64         `json:"runMs"`
	SlotWaitMs  *int64         `json:"slotWaitMs"`
	SlowestStep *NamedDuration `json:"slowestStep"`
}

// ModeStats is slot time and Tests time for passing runs of one test mode.
type ModeStats struct {
	Runs  int     `json:"runs"`
	Run   Summary `json:"run"`
	Tests Summary `json:"tests"`
}

// TestModeStats splits ModeStats by test mode.
type TestModeStats struct {
	Sequential ModeStats `json:"sequential"`
	Parallel   ModeStats `json:"parallel"`
}

// Bottlenecks is everything the bottleneck panel shows.
type Bottlenecks struct {
	Runs       int           `json:"runs"`
	Passed     int           `json:"passed"`
	Steps      []StepStat    `json:"steps"`
	SlotWait   Summary       `json:"slotWait"`
	Run        Summary       `json:"run"`
	Total      Summary       `json:"total"`
	ByTestMode TestModeStats `json:"byTestMode"`
	Slowest    []SlowRun     `json:"slowest"`
}

// LiveStep is a running run's step with elapsed and typical times.
type LiveStep struct {
	RunStep
	ElapsedMs *int64   `json:"elapsedMs"`
	TypicalMs *float64 `json:"typicalMs"`
}

// PendingStep is a step a running run has not started, by name.
type PendingStep struct {
	Name      string   `json:"name"`
	TypicalMs *float64 `json:"typicalMs"`
}

// RunningView is a running or abandoned run as the page shows it.
type RunningView struct {
	Run
	// TestMode is only what this run's own suites have shown.
	TestMode  *TestMode     `json:"testMode"`
	ElapsedMs *int64        `json:"elapsedMs"`
	Steps     []LiveStep    `json:"steps"`
	Pending   []PendingStep `json:"pending"`
}

// RecentView is a finished run with its test mode.
type RecentView struct {
	Run
	TestMode *TestMode `json:"testMode"`
}

// QueueSlot is one held slot, from the queue state.
type QueueSlot struct {
	Slot      int    `json:"slot"`
	Owner     string `json:"owner"`
	RepoKey   string `json:"repoKey"`
	Repo      string `json:"repo"`
	StartedAt int64  `json:"startedAt"`
}

// QueueTicket is one waiter, from the queue state, in serving order.
type QueueTicket struct {
	Name     string `json:"name"`
	RepoKey  string `json:"repoKey"`
	Repo     string `json:"repo"`
	QueuedAt int64  `json:"queuedAt"`
}

// WaitingView is one waiter as the page shows it. Position 1 is next.
type WaitingView struct {
	Position int     `json:"position"`
	Name     string  `json:"name"`
	RepoKey  string  `json:"repoKey"`
	Repo     string  `json:"repo"`
	Branch   *string `json:"branch"`
	Subject  *string `json:"subject"`
	Worktree *string `json:"worktree"`
	QueuedAt int64   `json:"queuedAt"`
	WaitMs   int64   `json:"waitMs"`
}

// HistoryCounts is how much the history holds.
type HistoryCounts struct {
	Runs  int `json:"runs"`
	Steps int `json:"steps"`
}

// StateInput is everything BuildDashboardState needs.
type StateInput struct {
	// Runs is every run the run logs describe right now.
	Runs []Run
	// Slots and Tickets are the queue's state; Tickets in serving order.
	Slots   []QueueSlot
	Tickets []QueueTicket
	// Recent is the newest finished runs for the results list; WindowRuns
	// the newest runs that took a slot, which the statistics cover in full.
	Recent        []Run
	WindowRuns    []Run
	NowMs         int64
	SlotCount     int
	HistoryCounts HistoryCounts
}

// DashboardState is the JSON the page polls.
type DashboardState struct {
	GeneratedAt int64         `json:"generatedAt"`
	SlotCount   int           `json:"slotCount"`
	Slots       []QueueSlot   `json:"slots"`
	Running     []RunningView `json:"running"`
	// HeldWithoutRun is slots held by `commit-queue acquire`, which writes
	// no run log, so the page can still show who holds them.
	HeldWithoutRun []QueueSlot   `json:"heldWithoutRun"`
	Abandoned      []RunningView `json:"abandoned"`
	Waiting        []WaitingView `json:"waiting"`
	Recent         []RecentView  `json:"recent"`
	Bottlenecks    Bottlenecks   `json:"bottlenecks"`
	History        HistoryCounts `json:"history"`
}
