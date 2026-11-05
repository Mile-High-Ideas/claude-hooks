package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// AppCheckResult holds the result of checking a single app for a single phase
// (lint or typecheck). Errors is the parsed count from the phase's output;
// Err is non-nil on any failure.
type AppCheckResult struct {
	AppName string
	Output  string
	Err     error
	Errors  int
}

// extractErrorCount parses the error count from formatted error messages
// like "found 5 typecheck error(s)" or "found 3 lint error(s)".
func extractErrorCount(err error) int {
	if err == nil {
		return 0
	}
	var count int
	if _, scanErr := fmt.Sscanf(err.Error(), "found %d ", &count); scanErr == nil {
		return count
	}
	return 1 // fallback: at least 1 error if the function returned an error
}

// failureDetail is the status-line text for a failed check: "N errors" when
// err reports a count ("found N ..."), otherwise err's own message. A check
// that could not start (compiler not installed, say) has no count, and
// printing "1 errors" for it hides the reason in compact mode, where nothing
// else of the check's output reaches the terminal.
func failureDetail(err error) string {
	var count int
	if _, scanErr := fmt.Sscanf(err.Error(), "found %d ", &count); scanErr == nil {
		return fmt.Sprintf("%d errors", count)
	}
	return err.Error()
}

// phaseJob represents a single app's participation in one phase (lint or typecheck).
type phaseJob struct {
	name    string
	config  AppConfig
	files   []string
	full    bool // true = full-project phase, false = incremental (changed files only)
	skipped bool // true = this phase is skipped for this app (e.g. SkipLint + lint phase)
}

// collectJobs builds the per-app job list for a phase. A job is included when
// fullLintOnCommit is set, when a shared path changed, or when the app has its
// own staged files. Jobs inherit each app's skip flags via the skipped field so
// the runner can still emit a "skipped" status line in the report.
func collectJobs(apps map[string]AppConfig, appFiles map[string][]string, sharedChanged, fullLintOnCommit bool, appSkipped func(AppConfig) bool) []phaseJob {
	var jobs []phaseJob
	for appName, appConfig := range apps {
		files := appFiles[appName]

		if fullLintOnCommit {
			jobs = append(jobs, phaseJob{name: appName, config: appConfig, files: files, full: true, skipped: appSkipped(appConfig)})
		} else if sharedChanged || len(files) > 0 {
			jobs = append(jobs, phaseJob{name: appName, config: appConfig, files: files, full: sharedChanged, skipped: appSkipped(appConfig)})
		}
	}
	return jobs
}

// filterLintableFiles filters to only .ts/.tsx/.js/.jsx files
func filterLintableFiles(files []string) []string {
	var lintFiles []string
	for _, f := range files {
		ext := strings.ToLower(f)
		if strings.HasSuffix(ext, ".ts") || strings.HasSuffix(ext, ".tsx") ||
			strings.HasSuffix(ext, ".js") || strings.HasSuffix(ext, ".jsx") {
			lintFiles = append(lintFiles, f)
		}
	}
	return lintFiles
}

// ===========================================================================
// LINT PHASE
// ===========================================================================

// runLint runs oxlint/eslint across all affected apps in parallel. Incremental
// mode (no fullLintOnCommit, no shared-path change) short-circuits: lint-staged
// has already run on the staged files, so there's nothing to do.
//
// Reports land under $reportDir/lint/<app>.txt.
func runLint(apps map[string]AppConfig, appFiles map[string][]string, sharedChanged bool, lintFilter LintFilter, fullLintOnCommit bool, packageManager string) error {
	return runLintTo(os.Stdout, apps, appFiles, sharedChanged, lintFilter, fullLintOnCommit, packageManager)
}

// runLintTo is the io.Writer-parameterized variant used when the top-level
// caller wants to run lint + typecheck concurrently and print their sections
// in deterministic order after both have finished.
func runLintTo(w io.Writer, apps map[string]AppConfig, appFiles map[string][]string, sharedChanged bool, lintFilter LintFilter, fullLintOnCommit bool, packageManager string) error {
	if !compactMode() {
		_, _ = fmt.Fprintln(w, "================================")
		_, _ = fmt.Fprintln(w, "  LINT (PARALLEL)")
		_, _ = fmt.Fprintln(w, "================================")
	}

	jobs := collectJobs(apps, appFiles, sharedChanged, fullLintOnCommit, func(a AppConfig) bool { return a.SkipLint })

	if len(jobs) == 0 {
		if !compactMode() {
			_, _ = fmt.Fprintln(w, "No apps to lint")
		}
		return nil
	}

	if !compactMode() {
		_, _ = fmt.Fprintf(w, "Linting %d app(s) in parallel...\n\n", len(jobs))
	}

	var wg sync.WaitGroup
	results := make([]AppCheckResult, len(jobs))

	for i, job := range jobs {
		wg.Add(1)
		go func(idx int, j phaseJob) {
			defer wg.Done()

			// Per-app timing: each goroutine emits its own start/end lines
			// directly to stdout in compact mode so the user sees live
			// progress instead of a long silent wait. The phase-level
			// status line still summarizes at the end.
			appCheck := "Lint " + j.name
			printStart(appCheck)

			var output bytes.Buffer
			var err error
			var errs int

			if j.skipped {
				fmt.Fprintf(&output, "   ⏩ %s lint skipped (skipLint: true)\n", j.name)
				printStatus(appCheck, true, "skipped")
			} else if j.full {
				fmt.Fprintf(&output, "🔍 Running full lint for %s...\n", j.name)
				lintOutput, lintErr := runFilteredLintBuffered(j.name, j.config.Path, lintFilter)
				output.WriteString(lintOutput)
				if lintErr != nil {
					fmt.Fprintf(&output, "   ❌ %s lint failed\n", j.name)
					errs = extractErrorCount(lintErr)
					err = lintErr
					printStatus(appCheck, false, failureDetail(lintErr))
				} else {
					fmt.Fprintf(&output, "   ✓ %s passed lint\n", j.name)
					printStatus(appCheck, true, "")
				}
			} else {
				// Incremental: lint-staged already ran on the staged files.
				// Nothing to do at this layer unless fullLintOnCommit is set.
				lintFiles := filterLintableFiles(j.files)
				if len(lintFiles) == 0 {
					fmt.Fprintf(&output, "   No lintable files in %s\n", j.name)
					printStatus(appCheck, true, "no files")
				} else {
					fmt.Fprintf(&output, "   ✓ %s lint handled by lint-staged (%d files)\n", j.name, len(lintFiles))
					printStatus(appCheck, true, fmt.Sprintf("%d files via lint-staged", len(lintFiles)))
				}
			}

			results[idx] = AppCheckResult{
				AppName: j.name,
				Output:  output.String(),
				Err:     err,
				Errors:  errs,
			}
		}(i, job)
	}

	// Run convex/backend ESLint in parallel if an eslint config exists there.
	// Convex ESLint is a lint-only check; it has no typecheck counterpart.
	var convexResult AppCheckResult
	if findConvexEslintPath() != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := runConvexEslintBuffered(lintFilter)
			convexResult = AppCheckResult{
				AppName: findConvexEslintPath() + " (eslint)",
				Output:  out,
				Err:     err,
			}
		}()
	}

	wg.Wait()

	if convexResult.Output != "" || convexResult.Err != nil {
		results = append(results, convexResult)
	}

	return finalizePhaseResults(w, "Lint", "lint/", results, len(jobs))
}

// ===========================================================================
// TYPECHECK PHASE
// ===========================================================================

// runTypecheck runs tsc (or tsgo when TypecheckFilter.UseTsgo is true) across
// all affected apps in parallel.
//
// Full mode: `tsc --noEmit` (or `-b`) at the app root. Incremental mode:
// `tsc-files` on just the changed files.
//
// Reports land under $reportDir/typecheck/<app>.txt.
func runTypecheck(apps map[string]AppConfig, appFiles map[string][]string, sharedChanged bool, typecheckFilter TypecheckFilter, fullLintOnCommit bool, packageManager string) error {
	return runTypecheckTo(os.Stdout, apps, appFiles, sharedChanged, typecheckFilter, fullLintOnCommit, packageManager)
}

// runTypecheckTo is the io.Writer-parameterized variant; see runLintTo.
func runTypecheckTo(w io.Writer, apps map[string]AppConfig, appFiles map[string][]string, sharedChanged bool, typecheckFilter TypecheckFilter, fullLintOnCommit bool, packageManager string) error {
	if !compactMode() {
		_, _ = fmt.Fprintln(w, "================================")
		_, _ = fmt.Fprintln(w, "  TYPECHECK (PARALLEL)")
		_, _ = fmt.Fprintln(w, "================================")
	}

	jobs := collectJobs(apps, appFiles, sharedChanged, fullLintOnCommit, func(a AppConfig) bool { return a.SkipTypecheck })

	if len(jobs) == 0 {
		if !compactMode() {
			_, _ = fmt.Fprintln(w, "No apps to typecheck")
		}
		return nil
	}

	if !compactMode() {
		_, _ = fmt.Fprintf(w, "Typechecking %d app(s) in parallel...\n\n", len(jobs))
	}

	var wg sync.WaitGroup
	results := make([]AppCheckResult, len(jobs))

	for i, job := range jobs {
		wg.Add(1)
		go func(idx int, j phaseJob) {
			defer wg.Done()

			appCheck := "Typecheck " + j.name
			printStart(appCheck)

			var output bytes.Buffer
			var err error
			var errs int

			// Merge global typecheck filter with per-app override
			effectiveFilter := GetTypecheckFilter(typecheckFilter, j.config.TypecheckFilter)

			if j.skipped {
				fmt.Fprintf(&output, "   ⏩ %s typecheck skipped (skipTypecheck: true)\n", j.name)
				printStatus(appCheck, true, "skipped")
			} else if j.full {
				fmt.Fprintf(&output, "🔍 Running full typecheck for %s...\n", j.name)
				tcOutput, tcErr := runFilteredTypecheckBuffered(j.name, j.config.Path, j.config.Filter, packageManager, effectiveFilter, j.config.NodeMemoryMB)
				output.WriteString(tcOutput)
				if tcErr != nil {
					fmt.Fprintf(&output, "   ❌ %s typecheck failed\n", j.name)
					errs = extractErrorCount(tcErr)
					err = tcErr
					printStatus(appCheck, false, failureDetail(tcErr))
				} else {
					fmt.Fprintf(&output, "   ✓ %s passed typecheck\n", j.name)
					printStatus(appCheck, true, "")
				}
			} else {
				// Incremental typecheck on changed files only.
				lintFiles := filterLintableFiles(j.files)
				if len(lintFiles) == 0 {
					fmt.Fprintf(&output, "   No typecheckable files in %s\n", j.name)
					printStatus(appCheck, true, "no files")
				} else {
					fmt.Fprintf(&output, "🔍 Running incremental typecheck for %s (%d files)...\n", j.name, len(lintFiles))
					tcOutput, tcErr := runIncrementalTypecheckBuffered(j.config.Path, lintFiles, effectiveFilter)
					output.WriteString(tcOutput)
					if tcErr != nil {
						fmt.Fprintf(&output, "   ❌ %s incremental typecheck failed\n", j.name)
						errs = extractErrorCount(tcErr)
						err = tcErr
						printStatus(appCheck, false, failureDetail(tcErr))
					} else {
						fmt.Fprintf(&output, "   ✓ %s passed incremental typecheck\n", j.name)
						printStatus(appCheck, true, fmt.Sprintf("%d files", len(lintFiles)))
					}
				}
			}

			results[idx] = AppCheckResult{
				AppName: j.name,
				Output:  output.String(),
				Err:     err,
				Errors:  errs,
			}
		}(i, job)
	}

	wg.Wait()

	return finalizePhaseResults(w, "Typecheck", "typecheck/", results, len(jobs))
}

// finalizePhaseResults prints the per-app output, emits the compact status
// line, and returns an aggregate error if any app failed. Kept out of the
// phase runners so the two phases stay identical in shape — only the phase
// name, report-dir hint, and per-job logic differ.
func finalizePhaseResults(w io.Writer, phaseName, reportSubdir string, results []AppCheckResult, jobCount int) error {
	checksFailed := false
	var failedResults []AppCheckResult

	for _, result := range results {
		if compactMode() {
			if result.Err != nil {
				checksFailed = true
				failedResults = append(failedResults, result)
			}
		} else {
			if result.Output != "" {
				_, _ = fmt.Fprint(w, result.Output)
				_, _ = fmt.Fprintln(w)
			}
			if result.Err != nil {
				checksFailed = true
			}
		}
	}

	if compactMode() {
		if checksFailed {
			sort.Slice(failedResults, func(i, j int) bool {
				return failedResults[i].AppName < failedResults[j].AppName
			})
			parts := make([]string, 0, len(failedResults))
			for _, r := range failedResults {
				parts = append(parts, r.AppName+" "+failureDetail(r.Err))
			}
			printStatusTo(w, phaseName, false, strings.Join(parts, ", "))
			printReportHintTo(w, reportSubdir)
			return fmt.Errorf("%s failed", strings.ToLower(phaseName))
		}
		printStatusTo(w, phaseName, true, fmt.Sprintf("%d apps", jobCount))
		return nil
	}

	if checksFailed {
		_, _ = fmt.Fprintln(w, "================================")
		_, _ = fmt.Fprintf(w, "  %s FAILED\n", strings.ToUpper(phaseName))
		_, _ = fmt.Fprintln(w, "================================")
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "Fix the errors above and try again")
		_, _ = fmt.Fprintln(w)
		return fmt.Errorf("%s failed", strings.ToLower(phaseName))
	}
	return nil
}

// RunLintAndTypecheckConcurrent fires lint and typecheck as top-level
// goroutines so their CPU work overlaps, while buffering each phase's
// output so the final stdout stays ordered (lint block, then typecheck).
// Returns the first error encountered (both are still run to completion).
func RunLintAndTypecheckConcurrent(apps map[string]AppConfig, appFiles map[string][]string, sharedChanged bool, typecheckFilter TypecheckFilter, lintFilter LintFilter, fullLintOnCommit bool, packageManager string) (lintErr error, typecheckErr error) {
	var lintBuf, typecheckBuf bytes.Buffer
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		lintErr = runLintTo(&lintBuf, apps, appFiles, sharedChanged, lintFilter, fullLintOnCommit, packageManager)
	}()
	go func() {
		defer wg.Done()
		typecheckErr = runTypecheckTo(&typecheckBuf, apps, appFiles, sharedChanged, typecheckFilter, fullLintOnCommit, packageManager)
	}()
	wg.Wait()

	fmt.Print(lintBuf.String())
	fmt.Print(typecheckBuf.String())
	return lintErr, typecheckErr
}
