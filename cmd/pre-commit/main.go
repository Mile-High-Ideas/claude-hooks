package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CLI flags
var (
	standalone  bool
	targetPath  string
	checkName   string
	listChecks  bool
	verboseFlag bool
	configPath  string
	reportDir   string
	noLock      bool
)

func init() {
	flag.BoolVar(&standalone, "standalone", false, "Run without git context (check all files in path)")
	flag.StringVar(&targetPath, "path", "", "Directory path to check (used with --standalone)")
	flag.StringVar(&checkName, "check", "", "Run only a specific check (e.g., frontendStructure, srp, mockCheck)")
	flag.BoolVar(&listChecks, "list", false, "List available checks")
	flag.StringVar(&configPath, "config", "", "Path to .pre-commit.json config file (defaults to .pre-commit.json in target path)")
	flag.StringVar(&reportDir, "report-dir", "", "Directory to write detailed lint/typecheck reports (creates lint/ and typecheck/ subdirs)")
	flag.BoolVar(&noLock, "no-lock", false, "Skip exclusive lock (allow concurrent runs)")
	flag.BoolVar(&verboseFlag, "verbose", false, "Print full per-app output even when reports are being written. Default: compact status lines when report-dir is set.")
}

// compactMode returns true when reports are being written to a directory AND
// -verbose was not passed. Verbose overrides compaction so callers can see the
// full per-app output even while reports are also written to disk.
func compactMode() bool {
	if verboseFlag {
		return false
	}
	return reportDir != ""
}

// absStaged converts staged file paths to absolute using projectRoot.
func absStaged(files []string, projectRoot string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if filepath.IsAbs(f) {
			out = append(out, f)
		} else {
			out = append(out, filepath.Join(projectRoot, f))
		}
	}
	return out
}

// checkKeyToDisplay maps the config key used in features / warningChecks
// to the display name each check passes to printStatus. Keeping the mapping
// central lets printStatus detect "this failing check is configured as a
// warning" by display name without threading the key through every caller.
var checkKeyToDisplay = map[string]string{
	"lint":                    "Lint",
	"typecheck":               "Typecheck",
	"lintStaged":              "Formatting",
	"consoleCheck":            "Console check",
	"dataLayerCheck":          "Data layer check",
	"nextImageCheck":          "Next image refs",
	"nextLinkCheck":           "Next link check",
	"changelog":               "Changelog",
	"goLint":                  "Go linting",
	"nativeBuild":             "Native build",
	"convexValidation":        "Convex validation",
	"buildCheck":              "Build check",
	"bundleCheck":             "Bundle check",
	"maestroValidation":       "Maestro validation",
	"frontendStructure":       "Frontend structure",
	"srp":                     "SRP compliance",
	"testFiles":               "Test files",
	"mockCheck":               "Mock check",
	"vitestAssertions":        "Vitest assertions",
	"testCoverage":            "Test coverage",
	"testQuality":             "Test quality",
	"stubTestCheck":           "Stub tests",
	"missingTestsCheck":       "Missing tests",
	"testSubstanceCheck":      "Test substance",
	"redundantCreatedAtCheck": "Redundant createdAt",
	"tiersGen":                "Tiers gen",
	"linguiExtract":           "Lingui extract",
	"tests":                   "Tests",
}

// warningDisplayNames holds the set of display names whose failures should
// render as ⚠️ (warning) rather than ❌. Populated from config.WarningChecks
// at the start of each run() via registerWarningChecks.
var warningDisplayNames = map[string]bool{}

// registerWarningChecks translates config.WarningChecks (a list of config
// keys like "maestroValidation") into the display-name set consulted by
// printStatus. Call once per run after config is loaded.
func registerWarningChecks(keys []string) {
	warningDisplayNames = map[string]bool{}
	for _, k := range keys {
		if name, ok := checkKeyToDisplay[k]; ok {
			warningDisplayNames[name] = true
		}
	}
}

// checkStarts records when each check began so the pass/fail status line can
// fold elapsed time into its detail string. Lint and typecheck run as
// concurrent goroutines under the same process, so access is guarded by
// checkStartsMu.
var (
	checkStartsMu sync.Mutex
	checkStarts   = map[string]time.Time{}
)

// printStart marks the moment a check began running. In compact mode it
// emits a "▶ HH:MM:SS Name" line so the user sees work in flight, not just
// the eventual result. The start time is stashed by name so printStatus /
// printWarningStatus can fold elapsed time into the end-of-check line.
func printStart(name string) {
	printStartTo(os.Stdout, name)
}

// printStartTo writes the start line to a specific writer. Used so the
// lint+typecheck concurrent runner can emit start lines directly to stdout
// before the buffered phase output is flushed.
func printStartTo(w io.Writer, name string) {
	now := time.Now()
	checkStartsMu.Lock()
	checkStarts[name] = now
	checkStartsMu.Unlock()
	if !compactMode() {
		return
	}
	_, _ = fmt.Fprintf(w, "  ▶ %s %s\n", now.Format("15:04:05"), name)
}

// consumeStart returns the start time recorded by printStart for name and
// clears it. The clear matters because printStatus and printWarningStatus
// can both fire for the same check (warning routing) and we only want
// timing folded in once.
func consumeStart(name string) (time.Time, bool) {
	checkStartsMu.Lock()
	defer checkStartsMu.Unlock()
	start, ok := checkStarts[name]
	if ok {
		delete(checkStarts, name)
	}
	return start, ok
}

// formatCheckDuration trims durations to a readable form for status lines.
// Sub-second runs render as "Nms"; anything longer collapses to one decimal
// of seconds (or whole seconds past 10s) so the line stays compact.
func formatCheckDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Truncate(time.Second).String()
}

// foldTimingIntoDetail prefixes "HH:MM:SS, Ns" onto detail when a printStart
// was recorded for name. Timing comes first so the eye lands on it; the
// caller's existing detail (e.g. "12 apps") trails. Returns detail unchanged
// when no start was recorded — keeps the helper safe for call sites that
// were not paired with printStart.
func foldTimingIntoDetail(name, detail string) string {
	start, ok := consumeStart(name)
	if !ok {
		return detail
	}
	timing := fmt.Sprintf("%s, %s", time.Now().Format("15:04:05"), formatCheckDuration(time.Since(start)))
	if detail == "" {
		return timing
	}
	return timing + ", " + detail
}

// printStatus prints a compact pass/fail status line for a check.
// name is the check name, passed indicates success, detail is optional context (e.g., error count).
//
// If the check is registered as a warning-only check via registerWarningChecks,
// a failing result is rendered with the ⚠️ (warning) marker instead of ❌
// so the visual signal matches the non-blocking routing that collectResult
// applies to the same check.
func printStatus(name string, passed bool, detail string) {
	printStatusTo(os.Stdout, name, passed, detail)
}

// printStatusTo writes a compact status line to the given writer. Used by the
// split lint/typecheck runners which buffer their output so phase sections
// print in a deterministic order even though they ran concurrently.
func printStatusTo(w io.Writer, name string, passed bool, detail string) {
	if !compactMode() {
		// Still drain the recorded start so the map stays clean across runs
		// of the same process (e.g. tests that exercise multiple checks).
		consumeStart(name)
		return
	}
	detail = foldTimingIntoDetail(name, detail)
	if !passed && warningDisplayNames[name] {
		// foldTimingIntoDetail already consumed the start, so the inner call
		// is a no-op for timing — it just re-uses the warning rendering.
		printWarningStatusTo(w, name, detail)
		return
	}
	icon := "✅"
	status := ""
	if !passed {
		icon = "❌"
	}
	if detail != "" {
		status = " (" + detail + ")"
	}
	_, _ = fmt.Fprintf(w, "  %s %s%s\n", icon, name, status)
}

// printWarningStatus prints a compact warning status line for a non-blocking check.
func printWarningStatus(name string, detail string) {
	printWarningStatusTo(os.Stdout, name, detail)
}

func printWarningStatusTo(w io.Writer, name string, detail string) {
	if !compactMode() {
		consumeStart(name)
		return
	}
	detail = foldTimingIntoDetail(name, detail)
	status := ""
	if detail != "" {
		status = " (" + detail + ")"
	}
	_, _ = fmt.Fprintf(w, "  ⚠️  %s%s (warning)\n", name, status)
}

// printReportHint prints a pointer to the report directory for a failed check.
func printReportHint(subdir string) {
	printReportHintTo(os.Stdout, subdir)
}

func printReportHintTo(w io.Writer, subdir string) {
	if compactMode() {
		_, _ = fmt.Fprintf(w, "     → %s/%s\n", reportDir, subdir)
	}
}

func main() {
	flag.Parse()

	if listChecks {
		printAvailableChecks()
		return
	}

	// Acquire exclusive lock to prevent concurrent pre-commit runs
	var lockFile *os.File
	if !noLock {
		var err error
		lockFile, err = acquireLock()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: pre-commit already running — commit rejected.")
			os.Exit(1)
		}
	}
	defer releaseLock(lockFile)

	// Set up report directory with branch name and timestamp
	if reportDir != "" {
		reportDir = setupReportDir(reportDir)
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// getLockPath returns a temp file path unique to the current git repo.
// In standalone mode with a target path, the lock is per-workspace so
// parallel turbo invocations across different workspaces don't block each other.
func getLockPath() string {
	// Use the git toplevel as the repo identifier
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	repoID := "unknown"
	if err == nil {
		repoID = strings.TrimSpace(string(out))
	}

	lockID := repoID
	if standalone && targetPath != "" {
		absPath, err := filepath.Abs(targetPath)
		if err == nil {
			lockID = absPath
		}
	}

	hash := sha256.Sum256([]byte(lockID))
	return filepath.Join(os.TempDir(), fmt.Sprintf("pre-commit-%x.lock", hash[:8]))
}

// setupReportDir creates a subdirectory with branch name and timestamp
func setupReportDir(baseDir string) string {
	branch := getGitBranch()
	timestamp := time.Now().Format("20060102_150405")

	// Sanitize branch name for filesystem (replace / with -)
	safeBranch := strings.ReplaceAll(branch, "/", "-")

	subDir := fmt.Sprintf("%s_%s", safeBranch, timestamp)
	fullPath := filepath.Join(baseDir, subDir)

	if err := os.MkdirAll(fullPath, 0755); err != nil {
		fmt.Printf("Warning: failed to create report directory: %v\n", err)
		return baseDir
	}

	fmt.Printf("📁 Reports will be written to: %s\n\n", fullPath)
	return fullPath
}

// getGitBranch returns the current git branch name
func getGitBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func printAvailableChecks() {
	fmt.Println("Available checks:")
	fmt.Println("  frontendStructure  - Validate CRUD folder structure in components/")
	fmt.Println("  srp                - Single Responsibility Principle check (TypeScript)")
	fmt.Println("  srpNative          - Structural SRP for Swift/Kotlin (file/type/function length, one type per file)")
	fmt.Println("  mockCheck          - Ensure tests use __mocks__/ instead of inline mocks")
	fmt.Println("  consoleCheck       - Check for console.log statements")
	fmt.Println("  lint               - Run oxlint/eslint across all affected apps")
	fmt.Println("  typecheck          - Run tsc (or tsgo) across all affected apps")
	fmt.Println("  tests              - Run test suites")
	fmt.Println("  changelog          - Validate changelog entries")
	fmt.Println("  goLint             - Go linting (if enabled)")
	fmt.Println("  nativeBuild        - Native app compilation check (iOS/Android)")
	fmt.Println("  convexValidation   - Convex schema validation (if enabled)")
	fmt.Println("  buildCheck         - Build verification (if enabled)")
	fmt.Println("  bundleCheck        - Run app bundlers (e.g. expo export) without native compile")
	fmt.Println("  vitestAssertions   - Ensure vitest configs have requireAssertions: true")
	fmt.Println("  testCoverage       - Ensure source files have corresponding test files")
	fmt.Println("  testQuality        - Ban export-only stub tests (toBeDefined/typeof checks)")
	fmt.Println("  stubTestCheck      - Ban placeholder expect(true).toBe(true) stub tests")
	fmt.Println("  missingTestsCheck  - Ban source files without co-located .test.ts(x) (per-app scoped)")
	fmt.Println("  testSubstanceCheck - LOC-ratio / interaction / branch / tautology gates against (source, test) pairs")
	fmt.Println("  redundantCreatedAtCheck - Ban createdAt fields inside Convex defineTable (use _creationTime)")
	fmt.Println("  dataLayerCheck     - Check for direct Convex imports (should use data-layer)")
	fmt.Println("  maestroValidation  - Validate Maestro flow id: selectors resolve to source testIDs")
	fmt.Println("  nextImageCheck     - Verify Next.js public/ asset references resolve (static)")
	fmt.Println("  nextLinkCheck      - Verify Next.js internal links resolve (static/crawl/both)")
}

func run() error {
	// Handle standalone mode
	if standalone {
		return runStandalone()
	}

	fmt.Println("Running pre-commit checks...")
	fmt.Println()

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Register warning-only checks so printStatus downgrades their failures
	// from ❌ to ⚠️ and matches the routing collectResult applies.
	registerWarningChecks(config.WarningChecks)

	// Set up report directory from config if not provided via flag
	if reportDir == "" && config.ReportDir != "" {
		reportDir = setupReportDir(config.ReportDir)
	}

	// Get staged files
	stagedFiles, err := getStagedFiles()
	if err != nil {
		return fmt.Errorf("failed to get staged files: %w", err)
	}

	if len(stagedFiles) == 0 {
		fmt.Println("No staged files to check")
		return nil
	}

	// Categorize files by app
	appFiles, sharedChanged := categorizeFiles(stagedFiles, config.Apps, config.SharedPaths)

	// Print detection summary
	printDetectionSummary(appFiles, sharedChanged)

	// If a specific check is requested, run only that check
	if checkName != "" {
		return runSpecificCheck(checkName, config, stagedFiles)
	}

	// =====================================================================
	// PHASE 1 — Hard gates that must pass before any work runs.
	// =====================================================================

	// Branch protection
	if config.Features.BranchProtection {
		if err := checkBranchProtection(config.ProtectedBranches); err != nil {
			return err
		}
	}

	// =====================================================================
	// PHASE 2 — Sequential prerequisites.
	//
	//   - Changelog: cheapest possible gate; if a fragment is missing the
	//     commit is going to fail anyway, so bail before burning CPU on
	//     lint/typecheck/tests.
	//   - lint-staged: auto-formats staged files. Must run before any
	//     read-based check or readers race against the formatter.
	//   - tiersGen: regenerates a derived TypeScript file. Must run before
	//     typecheck reads the regenerated output.
	//
	// All three fail fast on error.
	// =====================================================================

	if config.Features.Changelog {
		printStart("Changelog")
		if err := checkChangelog(stagedFiles, config.ChangelogExclude, config.ChangelogConfig, config.Apps); err != nil {
			if compactMode() {
				printStatus("Changelog", false, "missing fragments")
			}
			return err
		}
		printStatus("Changelog", true, "")
	}

	if config.Features.LintStaged {
		printStart("Formatting")
		if err := runLintStaged(config.LintStagedConfig); err != nil {
			return err
		}
	}

	if config.Features.TiersGen {
		printStart("Tiers gen")
		projectRoot, _ := os.Getwd()
		if err := checkTiersGen(projectRoot, stagedFiles); err != nil {
			fmt.Fprintf(os.Stderr, "Tiers gen failed: %v\n", err)
			return err
		}
	}

	// linguiExtract: regenerates message catalogs (.po + compiled .ts) for any
	// package whose staged source changed. Like tiersGen, must run before
	// typecheck reads the regenerated output.
	if config.Features.LinguiExtract {
		printStart("Lingui extract")
		projectRoot, _ := os.Getwd()
		if err := checkLinguiExtract(projectRoot, stagedFiles); err != nil {
			fmt.Fprintf(os.Stderr, "Lingui extract failed: %v\n", err)
			return err
		}
	}

	// =====================================================================
	// PHASE 3 — Everything else, fully async.
	//
	// Each check runs in its own goroutine. Status lines emit live to
	// stdout as each goroutine finishes — order is intentionally
	// non-deterministic. Errors and warnings are aggregated under
	// resultsMu and reported once after all goroutines drain.
	// =====================================================================

	var allErrors []string
	var allWarnings []string
	var resultsMu sync.Mutex
	var asyncWg sync.WaitGroup

	collectResult := func(checkName string, err error) {
		if err == nil {
			return
		}
		msg := fmt.Sprintf("%s: %v", checkName, err)
		resultsMu.Lock()
		defer resultsMu.Unlock()
		if config.IsWarningCheck(checkName) {
			allWarnings = append(allWarnings, msg)
		} else {
			allErrors = append(allErrors, msg)
		}
	}

	// asyncCheck launches fn as a goroutine. printStart is called inside the
	// goroutine so the start clock matches the moment work actually begins
	// (not the dispatch order). The runner is expected to print its own
	// pass/fail status line via printStatus / printWarningStatus.
	asyncCheck := func(displayName, configKey string, fn func() error) {
		asyncWg.Add(1)
		go func() {
			defer asyncWg.Done()
			printStart(displayName)
			collectResult(configKey, fn())
		}()
	}

	if config.Features.GoLint {
		asyncCheck("Go linting", "goLint", func() error {
			if !compactMode() {
				fmt.Println("================================")
				fmt.Println("  GO LINTING")
				fmt.Println("================================")
			}
			if err := checkGoLint(stagedFiles, config.GoLint); err != nil {
				printStatus("Go linting", false, "")
				return err
			}
			if !compactMode() {
				fmt.Println("Go linting passed")
				fmt.Println()
			} else {
				printStatus("Go linting", true, "")
			}
			return nil
		})
	}

	if config.Features.NativeBuild {
		asyncCheck("Native build", "nativeBuild", func() error {
			if !compactMode() {
				fmt.Println("================================")
				fmt.Println("  NATIVE BUILD CHECK")
				fmt.Println("================================")
			}
			if err := checkNativeBuild(stagedFiles, config.NativeBuild); err != nil {
				printStatus("Native build", false, "")
				return err
			}
			if !compactMode() {
				fmt.Println("Native build check passed")
				fmt.Println()
			} else {
				printStatus("Native build", true, "")
			}
			return nil
		})
	}

	if config.Features.ConvexValidation {
		asyncCheck("Convex validation", "convexValidation", func() error {
			if !compactMode() {
				fmt.Println("================================")
				fmt.Println("  CONVEX VALIDATION")
				fmt.Println("================================")
			}
			if err := checkConvex(config.Convex); err != nil {
				printStatus("Convex validation", false, "")
				return err
			}
			if !compactMode() {
				fmt.Println("Convex validation passed")
				fmt.Println()
			} else {
				printStatus("Convex validation", true, "")
			}
			return nil
		})
	}

	// Lint and Typecheck — each runs in its own goroutine and internally
	// fans out per-app goroutines. Per-app start/end lines emit live; the
	// phase-level summary appears at the end of each goroutine.
	if config.Features.Lint {
		asyncCheck("Lint", "lint", func() error {
			return runLint(config.Apps, appFiles, sharedChanged, config.LintFilter, config.Features.FullLintOnCommit, config.PackageManager)
		})
	}
	if config.Features.Typecheck {
		asyncCheck("Typecheck", "typecheck", func() error {
			return runTypecheck(config.Apps, appFiles, sharedChanged, config.TypecheckFilter, config.Features.FullLintOnCommit, config.PackageManager)
		})
	}

	if config.Features.ConsoleCheck {
		asyncCheck("Console check", "consoleCheck", func() error {
			return runConsoleCheck(appFiles, config.ConsoleAllowed)
		})
	}

	if config.Features.DataLayerCheck {
		asyncCheck("Data layer check", "dataLayerCheck", func() error {
			return runDataLayerCheck(appFiles, config.DataLayerAllowed)
		})
	}

	if config.Features.MaestroValidation {
		asyncCheck("Maestro validation", "maestroValidation", func() error {
			return runMaestroValidation(config.MaestroValidation)
		})
	}

	if config.Features.NextImageCheck {
		asyncCheck("Next image refs", "nextImageCheck", func() error {
			return runNextImageCheck(config.NextImageCheck, config.Apps)
		})
	}

	if config.Features.NextLinkCheck {
		asyncCheck("Next link check", "nextLinkCheck", func() error {
			return runNextLinkCheck(config.NextLinkCheck, config.Apps)
		})
	}

	if config.Features.FrontendStructure {
		asyncCheck("Frontend structure", "frontendStructure", func() error {
			return runFrontendStructureCheck(config.Apps, stagedFiles)
		})
	}

	if config.Features.SRP {
		asyncCheck("SRP compliance", "srp", func() error {
			var srpFiles []string
			fullMode := config.Features.FullSRPOnCommit

			if fullMode && len(config.SRPConfig.AppPaths) > 0 {
				for _, appPath := range config.SRPConfig.AppPaths {
					files, err := getAllFilesInPath(appPath)
					if err != nil {
						fmt.Printf("Warning: failed to get files from %s: %v\n", appPath, err)
						continue
					}
					srpFiles = append(srpFiles, files...)
				}
			} else {
				srpFiles = stagedFiles
			}

			// Populate newFiles/changedFiles for both testRequired (scope filter)
			// and errorScopes (severity escalation on new/changed files).
			newFiles, _ := getNewlyAddedFiles()
			changedFiles := make(map[string]bool, len(stagedFiles))
			for _, f := range stagedFiles {
				changedFiles[f] = true
			}

			srpCfg := config.SRPConfig.withStagedStrict(config.Features.SrpStrictOnStaged)
			filterResult := filterFilesForSRPWithDetails(srpFiles, srpCfg)
			return runSRPCheckWithFilter(filterResult, srpCfg, fullMode, newFiles, changedFiles)
		})
	}

	if config.Features.SrpNative {
		asyncCheck("SRP native compliance", "srpNative", func() error {
			return runSRPNativeCheck(stagedFiles, config.SRPNativeConfig)
		})
	}

	if config.Features.TestFiles {
		asyncCheck("Test files", "testFiles", func() error {
			return runTestFilesCheck(stagedFiles)
		})
	}

	if config.Features.MockCheck {
		asyncCheck("Mock check", "mockCheck", func() error {
			return runMockCheck(stagedFiles, config.MockCheck)
		})
	}

	if config.Features.VitestAssertions {
		asyncCheck("Vitest assertions", "vitestAssertions", func() error {
			return runVitestAssertionsCheck(config.Apps)
		})
	}

	if config.Features.TestCoverage {
		asyncCheck("Test coverage", "testCoverage", func() error {
			return runTestCoverageCheck(config.TestCoverageConfig)
		})
	}

	if config.Features.TestQuality {
		asyncCheck("Test quality", "testQuality", func() error {
			return runTestQualityCheck(config.TestQualityConfig)
		})
	}

	// Pre-resolve project root + absolutized staged files once so the
	// path-scoped checkers below don't each re-walk the working dir.
	projectRoot, _ := os.Getwd()
	stagedAbs := make([]string, 0, len(stagedFiles))
	for _, f := range stagedFiles {
		if filepath.IsAbs(f) {
			stagedAbs = append(stagedAbs, f)
		} else {
			stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
		}
	}

	if config.Features.StubTestCheck {
		asyncCheck("Stub tests", "stubTestCheck", func() error {
			return runStubTestCheck(config.StubTestCheckConfig, projectRoot, stagedAbs)
		})
	}

	if config.Features.MissingTestsCheck {
		asyncCheck("Missing tests", "missingTestsCheck", func() error {
			return runMissingTestsCheck(config.MissingTestsCheckConfig, projectRoot, stagedAbs)
		})
	}

	if config.Features.GoMissingTestsCheck {
		asyncCheck("Go missing tests", "goMissingTestsCheck", func() error {
			return runGoMissingTestsCheck(config.GoMissingTestsCheck, projectRoot, stagedAbs)
		})
	}

	if config.Features.GoTests {
		asyncCheck("Go tests", "goTests", func() error {
			return runGoTests(config.GoTests, projectRoot, stagedAbs)
		})
	}

	if config.Features.TestSubstanceCheck {
		asyncCheck("Test substance", "testSubstanceCheck", func() error {
			return runTestSubstanceCheck(config.TestSubstanceCheckConfig, projectRoot, stagedAbs)
		})
	}

	if config.Features.RedundantCreatedAtCheck {
		asyncCheck("Redundant createdAt", "redundantCreatedAtCheck", func() error {
			return runRedundantCreatedAtCheck(config.RedundantCreatedAtCheckConfig, projectRoot, stagedAbs)
		})
	}

	if config.Features.ConvexCheck {
		asyncCheck("Convex check", "convexCheck", func() error {
			return runConvexCheck(projectRoot, stagedAbs)
		})
	}

	if config.Features.BuildCheck {
		asyncCheck("Build check", "buildCheck", func() error {
			if !compactMode() {
				fmt.Println("================================")
				fmt.Println("  BUILD CHECK")
				fmt.Println("================================")
			}
			err := checkBuild(config.Build, config.Apps)
			if err != nil {
				printStatus("Build check", false, "")
			} else {
				printStatus("Build check", true, "")
			}
			if !compactMode() {
				fmt.Println()
			}
			return err
		})
	}

	if config.Features.BundleCheck {
		asyncCheck("Bundle check", "bundleCheck", func() error {
			err := runBundleCheck(config.BundleCheck, config.Apps, config.PackageManager)
			if err != nil {
				parts := strings.SplitN(err.Error(), ":", 2)
				detail := ""
				if len(parts) == 2 {
					detail = strings.TrimSpace(parts[1])
				}
				printStatus("Bundle check", false, detail)
			} else if len(config.BundleCheck.Apps) > 0 {
				printStatus("Bundle check", true, fmt.Sprintf("%d apps", len(config.BundleCheck.Apps)))
			}
			if !compactMode() {
				fmt.Println()
			}
			return err
		})
	}

	// Tests - run if global enabled OR any per-app override enables tests
	shouldRunTests := config.Features.Tests
	if !shouldRunTests {
		// Check if any app has tests explicitly enabled
		for _, override := range config.TestConfig.AppOverrides {
			if override.Enabled != nil && *override.Enabled {
				shouldRunTests = true
				break
			}
		}
	}
	if shouldRunTests {
		asyncCheck("Tests", "tests", func() error {
			testCtx := TestRunContext{
				AllApps:        config.Apps,
				AffectedApps:   appFiles,
				SharedChanged:  sharedChanged,
				Config:         config.TestConfig,
				GlobalEnabled:  config.Features.Tests,
				PackageManager: config.PackageManager,
				Env:            config.Env,
			}
			return runTests(testCtx)
		})
	}

	// Wait for every async check to drain before we report aggregated
	// errors and warnings. Status lines have already streamed to stdout in
	// the order checks finished.
	asyncWg.Wait()

	// Report warnings
	if len(allWarnings) > 0 {
		fmt.Println()
		if compactMode() {
			fmt.Printf("\n%d check(s) produced warnings (non-blocking)\n", len(allWarnings))
		} else {
			fmt.Println("================================")
			fmt.Println("  WARNINGS (non-blocking)")
			fmt.Println("================================")
			for _, w := range allWarnings {
				fmt.Printf("  ⚠️  %s\n", w)
			}
			fmt.Println()
		}
	}

	// Report all errors at the end
	fmt.Println()
	if len(allErrors) > 0 {
		fmt.Println("================================")
		fmt.Println("  PRE-COMMIT CHECKS FAILED")
		fmt.Println("================================")
		fmt.Println()
		if compactMode() {
			suffix := ""
			if len(allWarnings) > 0 {
				noun := "warning"
				if len(allWarnings) != 1 {
					noun = "warnings"
				}
				suffix = fmt.Sprintf(", %d %s", len(allWarnings), noun)
			}
			fmt.Printf("%d check(s) failed%s — see reports: %s\n", len(allErrors), suffix, reportDir)
		} else {
			fmt.Println("Fix the errors above and try again")
		}
		return fmt.Errorf("%d check(s) failed", len(allErrors))
	}

	fmt.Println("================================")
	fmt.Println("  ALL PRE-COMMIT CHECKS PASSED!")
	fmt.Println("================================")

	return nil
}

// printDetectionSummary prints a summary of detected file changes
func printDetectionSummary(appFiles map[string][]string, sharedChanged bool) {
	if sharedChanged {
		fmt.Println("Detected changes in shared packages or root files")
		fmt.Println("   This requires checking all apps completely")
		fmt.Println()
	} else {
		for appName, files := range appFiles {
			fmt.Printf("Detected %d changed file(s) in %s app\n", len(files), appName)
		}
		if len(appFiles) > 0 {
			fmt.Println()
		}
	}
}

// runStandalone runs checks in standalone mode (without git context)
func runStandalone() error {
	if targetPath == "" {
		return fmt.Errorf("--path is required when using --standalone")
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Verify path exists
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("path does not exist: %s", absPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", absPath)
	}

	fmt.Printf("Running standalone checks on: %s\n", absPath)
	fmt.Println()

	// Change to target directory to load config
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Find project root (where .pre-commit.json is)
	projectRoot := findProjectRoot(absPath)
	if projectRoot == "" {
		return fmt.Errorf("could not find .pre-commit.json in %s or any parent directory", absPath)
	}

	if err := os.Chdir(projectRoot); err != nil {
		return fmt.Errorf("failed to change to project root: %w", err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Register warning-only checks so printStatus downgrades their failures
	// from ❌ to ⚠️ and matches the routing collectResult applies.
	registerWarningChecks(config.WarningChecks)

	// Set up report directory from config if not provided via flag
	if reportDir == "" && config.ReportDir != "" {
		reportDir = setupReportDir(config.ReportDir)
	}

	// Get all files in the target path (simulate staged files)
	allFiles, err := getAllFiles(absPath, projectRoot)
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	fmt.Printf("Found %d files to check\n", len(allFiles))
	fmt.Println()

	// Scope config.Apps to only apps that overlap with the target path.
	// This prevents --path packages/types from triggering checks on all apps
	// just because packages/ is a shared path.
	relTarget, err := filepath.Rel(projectRoot, absPath)
	if err == nil && relTarget != "." {
		scopedApps := make(map[string]AppConfig)
		for name, app := range config.Apps {
			// Include app if target is inside the app, or app is inside the target
			if strings.HasPrefix(relTarget, app.Path) || strings.HasPrefix(app.Path, relTarget) {
				scopedApps[name] = app
			}
		}
		if len(scopedApps) > 0 {
			config.Apps = scopedApps
		} else {
			// Target doesn't match any configured app (e.g., packages/types).
			// Synthesize an app entry from the target's package.json so the
			// per-app lint/typecheck machinery can handle it directly.
			syntheticApp, ok := synthAppFromPackageJSON(absPath, relTarget)
			if ok {
				config.Apps = map[string]AppConfig{syntheticApp.name: syntheticApp.config}
			}
			// If no package.json either, keep all apps as fallback
		}
	}

	// Run specific check or all applicable checks
	if checkName != "" {
		return runSpecificCheck(checkName, config, allFiles)
	}

	// Run all enabled checks that make sense in standalone mode
	return runAllStandaloneChecks(config, allFiles)
}

// findProjectRoot finds the directory containing .pre-commit.json
func findProjectRoot(startPath string) string {
	current := startPath
	for {
		configFile := filepath.Join(current, ".pre-commit.json")
		if _, err := os.Stat(configFile); err == nil {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// getAllFiles recursively gets all files in a directory
// synthResult holds a synthesized app name and config
type synthResult struct {
	name   string
	config AppConfig
}

// synthAppFromPackageJSON reads the package.json in dir and creates a synthetic
// AppConfig so that standalone --path works for non-app packages.
func synthAppFromPackageJSON(dir, relPath string) (synthResult, bool) {
	pkgPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return synthResult{}, false
	}

	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Name == "" {
		return synthResult{}, false
	}

	// Derive a short name from the package name (e.g., "@dashtag/types" → "types")
	shortName := pkg.Name
	if idx := strings.LastIndex(shortName, "/"); idx >= 0 {
		shortName = shortName[idx+1:]
	}

	return synthResult{
		name: shortName,
		config: AppConfig{
			Path:   relPath,
			Filter: pkg.Name,
		},
	}, true
}

func getAllFiles(dir, projectRoot string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and node_modules
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path from project root
		relPath, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return err
		}

		files = append(files, relPath)
		return nil
	})

	return files, err
}

// getAllFilesInPath recursively gets all files in a directory, returning paths relative to current dir
func getAllFilesInPath(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and node_modules
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		files = append(files, path)
		return nil
	})

	return files, err
}

// runSpecificCheck runs a single named check
func runSpecificCheck(name string, config *Config, files []string) error {
	appFiles, sharedChanged := categorizeFiles(files, config.Apps, config.SharedPaths)

	switch name {
	case "frontendStructure":
		return runFrontendStructureCheck(config.Apps, files)
	case "srp":
		srpCfg := config.SRPConfig.withStagedStrict(config.Features.SrpStrictOnStaged)
		filterResult := filterFilesForSRPWithDetails(files, srpCfg)
		newFiles, _ := getNewlyAddedFiles()
		changedFiles := make(map[string]bool, len(files))
		for _, f := range files {
			changedFiles[f] = true
		}
		return runSRPCheckWithFilter(filterResult, srpCfg, true, newFiles, changedFiles)
	case "srpNative":
		return runSRPNativeCheck(files, config.SRPNativeConfig)
	case "mockCheck":
		return runMockCheck(files, config.MockCheck)
	case "consoleCheck":
		return runConsoleCheck(appFiles, config.ConsoleAllowed)
	case "lint":
		return runLint(config.Apps, appFiles, sharedChanged, config.LintFilter, config.Features.FullLintOnCommit, config.PackageManager)
	case "typecheck":
		return runTypecheck(config.Apps, appFiles, sharedChanged, config.TypecheckFilter, config.Features.FullLintOnCommit, config.PackageManager)
	case "tests":
		testCtx := TestRunContext{
			AllApps:        config.Apps,
			AffectedApps:   appFiles,
			SharedChanged:  sharedChanged,
			Config:         config.TestConfig,
			GlobalEnabled:  true, // When running --check tests, assume enabled
			PackageManager: config.PackageManager,
			Env:            config.Env,
		}
		return runTests(testCtx)
	case "changelog":
		return checkChangelog(files, config.ChangelogExclude, config.ChangelogConfig, config.Apps)
	case "goLint":
		return checkGoLint(files, config.GoLint)
	case "nativeBuild":
		return checkNativeBuild(files, config.NativeBuild)
	case "convexValidation":
		return checkConvex(config.Convex)
	case "buildCheck":
		return checkBuild(config.Build, config.Apps)
	case "bundleCheck":
		return runBundleCheck(config.BundleCheck, config.Apps, config.PackageManager)
	case "vitestAssertions":
		return runVitestAssertionsCheck(config.Apps)
	case "testCoverage":
		return runTestCoverageCheck(config.TestCoverageConfig)
	case "testQuality":
		return runTestQualityCheck(config.TestQualityConfig)
	case "stubTestCheck":
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		return runStubTestCheck(config.StubTestCheckConfig, projectRoot, stagedAbs)
	case "missingTestsCheck":
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		return runMissingTestsCheck(config.MissingTestsCheckConfig, projectRoot, stagedAbs)
	case "goMissingTestsCheck":
		projectRoot, _ := os.Getwd()
		return runGoMissingTestsCheck(config.GoMissingTestsCheck, projectRoot, absStaged(files, projectRoot))
	case "goTests":
		projectRoot, _ := os.Getwd()
		return runGoTests(config.GoTests, projectRoot, absStaged(files, projectRoot))
	case "testSubstanceCheck":
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		return runTestSubstanceCheck(config.TestSubstanceCheckConfig, projectRoot, stagedAbs)
	case "redundantCreatedAtCheck":
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		return runRedundantCreatedAtCheck(config.RedundantCreatedAtCheckConfig, projectRoot, stagedAbs)
	case "convexCheck":
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		return runConvexCheck(projectRoot, stagedAbs)
	case "tiersGen":
		projectRoot, _ := os.Getwd()
		return checkTiersGen(projectRoot, files)
	case "dataLayerCheck":
		return runDataLayerCheck(appFiles, config.DataLayerAllowed)
	case "maestroValidation":
		return runMaestroValidation(config.MaestroValidation)
	case "nextImageCheck":
		return runNextImageCheck(config.NextImageCheck, config.Apps)
	case "nextLinkCheck":
		return runNextLinkCheck(config.NextLinkCheck, config.Apps)
	default:
		return fmt.Errorf("unknown check: %s (use --list to see available checks)", name)
	}
}

// runAllStandaloneChecks runs all checks that work in standalone mode
// Collects all errors and continues running all checks before reporting
func runAllStandaloneChecks(config *Config, files []string) error {
	appFiles, sharedChanged := categorizeFiles(files, config.Apps, config.SharedPaths)
	printDetectionSummary(appFiles, sharedChanged)

	var allErrors []string
	var allWarnings []string

	collectResult := func(checkName string, err error) {
		if err == nil {
			return
		}
		msg := fmt.Sprintf("%s: %v", checkName, err)
		if config.IsWarningCheck(checkName) {
			allWarnings = append(allWarnings, msg)
		} else {
			allErrors = append(allErrors, msg)
		}
	}

	// Frontend structure check
	if config.Features.FrontendStructure {
		collectResult("frontendStructure", runFrontendStructureCheck(config.Apps, files))
	}

	// SRP check
	if config.Features.SRP {
		srpCfg := config.SRPConfig.withStagedStrict(config.Features.SrpStrictOnStaged)
		filterResult := filterFilesForSRPWithDetails(files, srpCfg)
		// In standalone mode there may be no git context; getNewlyAddedFiles
		// returns an empty/nil map and errorScopes simply has no effect.
		newFiles, _ := getNewlyAddedFiles()
		changedFiles := make(map[string]bool, len(files))
		for _, f := range files {
			changedFiles[f] = true
		}
		collectResult("srp", runSRPCheckWithFilter(filterResult, srpCfg, true, newFiles, changedFiles))
	}

	// Mock check
	if config.Features.MockCheck {
		collectResult("mockCheck", runMockCheck(files, config.MockCheck))
	}

	// Console check
	if config.Features.ConsoleCheck {
		collectResult("consoleCheck", runConsoleCheck(appFiles, config.ConsoleAllowed))
	}

	// Data layer check
	if config.Features.DataLayerCheck {
		collectResult("dataLayerCheck", runDataLayerCheck(appFiles, config.DataLayerAllowed))
	}

	// Next.js public-asset reference check
	if config.Features.NextImageCheck {
		collectResult("nextImageCheck", runNextImageCheck(config.NextImageCheck, config.Apps))
	}

	// Next.js internal-link check
	if config.Features.NextLinkCheck {
		collectResult("nextLinkCheck", runNextLinkCheck(config.NextLinkCheck, config.Apps))
	}

	// Maestro flow validation
	if config.Features.MaestroValidation {
		collectResult("maestroValidation", runMaestroValidation(config.MaestroValidation))
	}

	// Vitest assertions check
	if config.Features.VitestAssertions {
		collectResult("vitestAssertions", runVitestAssertionsCheck(config.Apps))
	}

	// Test coverage check
	if config.Features.TestCoverage {
		collectResult("testCoverage", runTestCoverageCheck(config.TestCoverageConfig))
	}

	// Test quality check
	if config.Features.TestQuality {
		collectResult("testQuality", runTestQualityCheck(config.TestQualityConfig))
	}

	// Stub test check
	if config.Features.StubTestCheck {
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		collectResult("stubTestCheck", runStubTestCheck(config.StubTestCheckConfig, projectRoot, stagedAbs))
	}

	// Missing tests check
	if config.Features.MissingTestsCheck {
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		collectResult("missingTestsCheck", runMissingTestsCheck(config.MissingTestsCheckConfig, projectRoot, stagedAbs))
	}

	// Test substance check
	if config.Features.TestSubstanceCheck {
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		collectResult("testSubstanceCheck", runTestSubstanceCheck(config.TestSubstanceCheckConfig, projectRoot, stagedAbs))
	}

	// Redundant createdAt check — bans `createdAt:` inside Convex
	// defineTable({...}) because Convex provides `_creationTime` for free.
	if config.Features.RedundantCreatedAtCheck {
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		collectResult("redundantCreatedAtCheck", runRedundantCreatedAtCheck(config.RedundantCreatedAtCheckConfig, projectRoot, stagedAbs))
	}

	// Convex check — runs the @milehighideas/oxlint-plugin-convex rules on
	// staged Convex files (gated by convexCheckConfig.severity / crudDomains).
	if config.Features.ConvexCheck {
		projectRoot, _ := os.Getwd()
		stagedAbs := make([]string, 0, len(files))
		for _, f := range files {
			if filepath.IsAbs(f) {
				stagedAbs = append(stagedAbs, f)
			} else {
				stagedAbs = append(stagedAbs, filepath.Join(projectRoot, f))
			}
		}
		collectResult("convexCheck", runConvexCheck(projectRoot, stagedAbs))
	}

	// Lint and typecheck — run concurrently, print in deterministic order
	if config.Features.Lint || config.Features.Typecheck {
		var lintErr, typecheckErr error
		if config.Features.Lint && config.Features.Typecheck {
			lintErr, typecheckErr = RunLintAndTypecheckConcurrent(config.Apps, appFiles, sharedChanged, config.TypecheckFilter, config.LintFilter, config.Features.FullLintOnCommit, config.PackageManager)
		} else if config.Features.Lint {
			lintErr = runLint(config.Apps, appFiles, sharedChanged, config.LintFilter, config.Features.FullLintOnCommit, config.PackageManager)
		} else {
			typecheckErr = runTypecheck(config.Apps, appFiles, sharedChanged, config.TypecheckFilter, config.Features.FullLintOnCommit, config.PackageManager)
		}
		if lintErr != nil {
			collectResult("lint", lintErr)
		}
		if typecheckErr != nil {
			collectResult("typecheck", typecheckErr)
		}
	}

	// Report warnings
	if len(allWarnings) > 0 {
		fmt.Println()
		fmt.Println("================================")
		fmt.Println("  WARNINGS (non-blocking)")
		fmt.Println("================================")
		for _, w := range allWarnings {
			fmt.Printf("  ⚠️  %s\n", w)
		}
	}

	fmt.Println()
	if len(allErrors) > 0 {
		fmt.Println("================================")
		fmt.Println("  STANDALONE CHECKS FAILED")
		fmt.Println("================================")
		fmt.Println()
		fmt.Println("Errors found:")
		for _, e := range allErrors {
			fmt.Printf("  • %s\n", e)
		}
		return fmt.Errorf("%d check(s) failed", len(allErrors))
	}

	fmt.Println("================================")
	fmt.Println("  ALL STANDALONE CHECKS PASSED!")
	fmt.Println("================================")

	return nil
}

// SRPFilterResult contains the result of filtering files for SRP
type SRPFilterResult struct {
	Files            []string
	SkippedByAppPath int
	SkippedByExclude int
	ExcludeMatches   map[string]int // excludePath -> count of files matched
	SkippedPaths     map[string]int // top-level directory -> count of files skipped
}

// filterFilesForSRP filters files based on SRP config (appPaths and excludePaths)
func filterFilesForSRP(files []string, config SRPConfig) []string {
	result := filterFilesForSRPWithDetails(files, config)
	return result.Files
}

// filterFilesForSRPWithDetails filters files and returns detailed information about what was filtered
func filterFilesForSRPWithDetails(files []string, config SRPConfig) SRPFilterResult {
	result := SRPFilterResult{
		ExcludeMatches: make(map[string]int),
		SkippedPaths:   make(map[string]int),
	}

	if len(config.AppPaths) == 0 && len(config.ExcludePaths) == 0 {
		result.Files = files
		return result
	}

	for _, file := range files {
		// Check if file is in an allowed app path
		if len(config.AppPaths) > 0 {
			inAllowedPath := false
			for _, appPath := range config.AppPaths {
				if strings.HasPrefix(file, appPath) {
					inAllowedPath = true
					break
				}
			}
			if !inAllowedPath {
				result.SkippedByAppPath++
				// Track which top-level directory was skipped
				topLevel := getTopLevelDir(file)
				result.SkippedPaths[topLevel]++
				continue
			}
		}

		// Check if file is in an excluded path
		excluded := false
		for _, excludePath := range config.ExcludePaths {
			if strings.HasPrefix(file, excludePath) || strings.Contains(file, excludePath) {
				excluded = true
				result.ExcludeMatches[excludePath]++
				break
			}
		}
		if excluded {
			result.SkippedByExclude++
			continue
		}

		result.Files = append(result.Files, file)
	}

	return result
}

// getTopLevelDir extracts the top-level directory from a file path
// e.g., "packages/ui/src/Button.tsx" -> "packages/"
// e.g., "apps/mobile/App.tsx" -> "apps/"
// e.g., "tsconfig.json" -> "(root)"
func getTopLevelDir(filePath string) string {
	parts := strings.Split(filePath, "/")
	if len(parts) <= 1 {
		return "(root)"
	}
	return parts[0] + "/"
}
