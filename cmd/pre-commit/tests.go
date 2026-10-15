package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TestRunContext provides context for determining which apps to test
type TestRunContext struct {
	AllApps        map[string]AppConfig
	AffectedApps   map[string][]string // appName -> list of changed files
	SharedChanged  bool
	Config         TestConfig
	GlobalEnabled  bool              // Global tests feature flag (can be overridden per-app)
	PackageManager string            // Package manager to use (pnpm, bun, npm, yarn)
	Env            map[string]string // Environment variables for commands
}

// runCapturedTestCommand runs one app's test command and returns its
// combined output. Tests replace it so they never spawn a real suite.
var runCapturedTestCommand = runCommandCapturedWithEnv

// appTestResult is the outcome of one app's test suite in compact mode.
type appTestResult struct {
	app           string
	output        string
	err           error
	retryAttempts int
}

// runTests runs tests based on configuration and affected files
func runTests(ctx TestRunContext) error {
	if !compactMode() {
		fmt.Println()
		fmt.Println("================================")
		fmt.Println("  TESTS")
		fmt.Println("================================")
	}

	appsToTest := determineAppsToTest(ctx)

	if len(appsToTest) == 0 {
		if !compactMode() {
			fmt.Println("No apps to test (all skipped or none affected)")
		}
		return nil
	}

	// Print what we're testing and why (verbose only)
	if !compactMode() {
		printTestPlan(ctx, appsToTest)
	}

	pm := ctx.PackageManager
	if pm == "" {
		pm = "pnpm"
	}
	appNames := sortedAppNames(appsToTest)

	if !compactMode() {
		return runTestsVerbose(ctx, pm, appNames, appsToTest)
	}

	var results []appTestResult
	if ctx.Config.Parallel {
		results = runAppTestsParallel(ctx, pm, appNames, appsToTest)
	} else {
		results = runAppTestsSequential(ctx, pm, appNames, appsToTest)
	}
	return summarizeTestResults(results)
}

// sortedAppNames returns the app names in a stable order, so runs and
// failure messages do not depend on map iteration.
func sortedAppNames(apps map[string]AppConfig) []string {
	names := make([]string, 0, len(apps))
	for name := range apps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// testCommandArgs builds the package-manager arguments for one app's tests.
func testCommandArgs(appConfig AppConfig) []string {
	// Use custom test command if specified, otherwise default to "test"
	testCmd := appConfig.TestCommand
	if testCmd == "" {
		testCmd = "test"
	}
	args := []string{"--filter", appConfig.Filter, testCmd}

	// Append per-app test args (e.g., --watchman=false for Jest)
	if len(appConfig.TestArgs) > 0 {
		args = append(args, "--")
		args = append(args, appConfig.TestArgs...)
	}
	return args
}

// runAppTestsSequential runs each app's suite after the previous one ends.
func runAppTestsSequential(ctx TestRunContext, pm string, appNames []string, apps map[string]AppConfig) []appTestResult {
	results := make([]appTestResult, 0, len(appNames))
	for _, appName := range appNames {
		results = append(results, runAppTestsCompact(ctx, pm, appName, apps[appName]))
	}
	return results
}

// runAppTestsParallel starts every app's suite at once and returns the
// results in appNames order once all of them have finished.
func runAppTestsParallel(ctx TestRunContext, pm string, appNames []string, apps map[string]AppConfig) []appTestResult {
	results := make([]appTestResult, len(appNames))
	var wg sync.WaitGroup
	for i, appName := range appNames {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = runAppTestsCompact(ctx, pm, appName, apps[appName])
		}()
	}
	wg.Wait()
	return results
}

// runAppTestsCompact runs one app's suite with output captured to its report
// file, retrying per TestConfig, and prints its start and status lines.
func runAppTestsCompact(ctx TestRunContext, pm, appName string, appConfig AppConfig) appTestResult {
	args := testCommandArgs(appConfig)

	// Per-app start line so each app's run shows up live in compact mode
	// instead of disappearing into the long Tests phase.
	appCheck := "Tests " + appName
	printStart(appCheck)

	output, err := runCapturedTestCommand(ctx.Env, pm, args...)

	// Retry policy: TestConfig.Retries gives every failed run a
	// chance to recover from environmental flake. Files listed in
	// TestConfig.FlakyTestFiles get one extra retry beyond that,
	// but only when the failure output mentions one of them — a
	// real regression in non-quarantined code still fails fast.
	retries := ctx.Config.Retries
	if err != nil && hasFlakyFailure(output, ctx.Config.FlakyTestFiles) {
		retries++
	}
	retryAttempts := 0
	for err != nil && retryAttempts < retries {
		retryAttempts++
		output, err = runCapturedTestCommand(ctx.Env, pm, args...)
	}

	writeTestReport(appName, output, err, reportDir)
	switch {
	case err != nil:
		printStatus(appCheck, false, fmt.Sprintf("%d failed", parseTestFailureCount(output)))
	case retryAttempts > 0:
		printStatus(appCheck, true, fmt.Sprintf("after %d retry", retryAttempts))
	default:
		printStatus(appCheck, true, "")
	}
	return appTestResult{app: appName, output: output, err: err, retryAttempts: retryAttempts}
}

// summarizeTestResults prints the overall Tests status line and returns an
// error naming every app whose suite failed.
func summarizeTestResults(results []appTestResult) error {
	var failed, passed, recovered []string
	for _, r := range results {
		if r.err != nil {
			if count := parseTestFailureCount(r.output); count > 0 {
				failed = append(failed, fmt.Sprintf("%s %d failed", r.app, count))
			} else {
				failed = append(failed, r.app+" failed")
			}
			continue
		}
		passed = append(passed, r.app)
		if r.retryAttempts > 0 {
			word := "retry"
			if r.retryAttempts != 1 {
				word = "retries"
			}
			recovered = append(recovered, fmt.Sprintf("%s after %d %s", r.app, r.retryAttempts, word))
		}
	}

	if len(failed) > 0 {
		printStatus("Tests", false, strings.Join(failed, ", "))
		printReportHint("tests/")
		failedApps := make([]string, 0, len(failed))
		for _, r := range results {
			if r.err != nil {
				failedApps = append(failedApps, r.app)
			}
		}
		return fmt.Errorf("%s tests failed", strings.Join(failedApps, ", "))
	}

	summary := strings.Join(passed, ", ")
	if len(recovered) > 0 {
		summary += " — recovered: " + strings.Join(recovered, ", ")
	}
	printStatus("Tests", true, summary)
	return nil
}

// runTestsVerbose streams each app's suite to the terminal, one at a time,
// and stops at the first app that still fails after its retries.
func runTestsVerbose(ctx TestRunContext, pm string, appNames []string, apps map[string]AppConfig) error {
	for _, appName := range appNames {
		appConfig := apps[appName]
		args := testCommandArgs(appConfig)
		appCheck := "Tests " + appName
		printStart(appCheck)

		fmt.Printf("\nRunning %s tests (command: %s)...\n", appName, args[2])
		err := runCommandWithEnv(ctx.Env, pm, args...)
		retries := ctx.Config.Retries
		retryAttempts := 0
		for err != nil && retryAttempts < retries {
			retryAttempts++
			fmt.Printf("\nRetrying %s tests (attempt %d/%d)...\n", appName, retryAttempts, retries)
			err = runCommandWithEnv(ctx.Env, pm, args...)
		}
		if err != nil {
			fmt.Printf("%s tests failed\n", appName)
			return fmt.Errorf("%s tests failed", appName)
		}
		if retryAttempts > 0 {
			fmt.Printf("%s tests passed (after %d retry)\n", appName, retryAttempts)
		} else {
			fmt.Printf("%s tests passed\n", appName)
		}
	}
	return nil
}

// hasFlakyFailure reports whether any of the configured flaky-test files
// appears in the test runner's failure output. We match against the file
// path because both Vitest and Jest print failed test specs as
// "FAIL <relative-path-to-test-file>". This lets us widen the retry
// budget *only* when the failure is in a quarantined file; failures
// elsewhere fail fast under the normal Retries policy.
func hasFlakyFailure(output string, flakyTestFiles []string) bool {
	if len(flakyTestFiles) == 0 {
		return false
	}
	for _, p := range flakyTestFiles {
		if strings.Contains(output, p) {
			return true
		}
	}
	return false
}

// testFailurePatterns matches the "Tests" summary line from both Vitest and Jest output.
// Vitest: "Tests  111 failed | 3878 passed"
// Jest:   "Tests:       4 failed, 965 passed, 969 total"
var testFailurePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)Tests[:\s]+(\d+)\s+failed`),
}

// parseTestFailureCount extracts the number of failed tests from test runner output.
// Returns 0 if the count cannot be determined.
func parseTestFailureCount(output string) int {
	for _, re := range testFailurePatterns {
		if m := re.FindStringSubmatch(output); len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 0
}

// writeTestReport writes test output to a report file
func writeTestReport(appName, output string, testErr error, baseDir string) {
	if baseDir == "" {
		return
	}

	// Full report (legacy content): banner + result + raw test output.
	var sb strings.Builder
	sb.WriteString(strings.Repeat("=", 80) + "\n")
	fmt.Fprintf(&sb, "TEST REPORT: %s\n", appName)
	fmt.Fprintf(&sb, "Generated: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	if testErr != nil {
		sb.WriteString("Result: FAILED\n")
	} else {
		sb.WriteString("Result: PASSED\n")
	}
	sb.WriteString(strings.Repeat("=", 80) + "\n\n")
	sb.WriteString(output)

	// Findings-only report: nothing on PASS; the failure-relevant lines on FAIL.
	var findings string
	if testErr != nil {
		body, count := extractErrorLines(output)
		if count == 0 {
			count = 1
		}
		findings = findingsDoc("TESTS", appName, count, body)
	}

	_ = writeDualReport(baseDir, "tests", appName, findings, sb.String())
}

// determineAppsToTest returns the apps that should have tests run based on config and context
func determineAppsToTest(ctx TestRunContext) map[string]AppConfig {
	result := make(map[string]AppConfig)

	// Check if shared paths changed and we should run all tests
	runAllDueToShared := ctx.SharedChanged && *ctx.Config.RunOnSharedChanges

	for appName, appConfig := range ctx.AllApps {
		// Determine if tests are enabled for this app
		testsEnabled := ctx.GlobalEnabled // Start with global setting

		// Check per-app override
		if override, exists := ctx.Config.AppOverrides[appName]; exists {
			// Check enabled override first (takes precedence)
			if override.Enabled != nil {
				testsEnabled = *override.Enabled
			} else if override.Skip {
				// Legacy skip field (deprecated but still supported)
				testsEnabled = false
			}
		}

		// Skip if tests not enabled for this app
		if !testsEnabled {
			continue
		}

		// Check per-app onlyWhenAffected override
		if override, exists := ctx.Config.AppOverrides[appName]; exists {
			if override.OnlyWhenAffected != nil {
				if *override.OnlyWhenAffected {
					// Only run if this app is affected (or shared changed and runOnSharedChanges is true)
					_, isAffected := ctx.AffectedApps[appName]
					if !isAffected && !runAllDueToShared {
						continue
					}
				}
				// If onlyWhenAffected is explicitly false, always run this app's tests
				result[appName] = appConfig
				continue
			}
		}

		// Apply global affectedOnly setting
		if ctx.Config.AffectedOnly {
			_, isAffected := ctx.AffectedApps[appName]
			if !isAffected && !runAllDueToShared {
				continue
			}
		}

		result[appName] = appConfig
	}

	return result
}

// printTestPlan prints information about what tests will run and why
func printTestPlan(ctx TestRunContext, appsToTest map[string]AppConfig) {
	if ctx.SharedChanged && *ctx.Config.RunOnSharedChanges {
		fmt.Println("Shared paths changed - running tests for all non-skipped apps")
	} else if ctx.Config.AffectedOnly {
		fmt.Println("Running tests only for affected apps:")
		for appName := range appsToTest {
			if _, affected := ctx.AffectedApps[appName]; affected {
				fmt.Printf("  - %s (has staged changes)\n", appName)
			} else {
				fmt.Printf("  - %s (explicitly configured to always run)\n", appName)
			}
		}
	} else {
		fmt.Println("Running tests for all configured apps...")
	}

	// Print skipped apps
	for appName := range ctx.AllApps {
		if _, willTest := appsToTest[appName]; !willTest {
			if override, exists := ctx.Config.AppOverrides[appName]; exists && override.Skip {
				fmt.Printf("  - %s (skipped via config)\n", appName)
			} else if ctx.Config.AffectedOnly {
				fmt.Printf("  - %s (skipped - not affected)\n", appName)
			}
		}
	}
}
