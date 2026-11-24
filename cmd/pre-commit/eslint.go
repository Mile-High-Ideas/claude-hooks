package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// lintError represents a parsed lint error (works for both ESLint and Oxlint)
type lintError struct {
	filePath string
	line     string
	column   string
	severity string // "error" or "warning"
	message  string
	rule     string
	fullText string
}

// DefaultLintExcludePaths are the path patterns excluded by default (empty - no filtering unless configured)
var DefaultLintExcludePaths = []string{}

// writeLintReport writes lint findings to a report file.
// linterName is used in the report header (e.g., "oxlint", "eslint").
func writeLintReport(appName, linterName, rawOutput string, allErrors, realErrors []lintError, baseDir string) error {
	// Count by severity
	warningCount := 0
	errorCount := 0
	for _, e := range realErrors {
		if e.severity == "warning" {
			warningCount++
		} else {
			errorCount++
		}
	}

	// Build the "findings by file" body once; it's shared by both the full
	// report and the findings-only report.
	errorsByFile := make(map[string][]lintError)
	for _, e := range realErrors {
		errorsByFile[e.filePath] = append(errorsByFile[e.filePath], e)
	}
	var body strings.Builder
	for file, errs := range errorsByFile {
		fileWarnings := 0
		fileErrors := 0
		for _, e := range errs {
			if e.severity == "warning" {
				fileWarnings++
			} else {
				fileErrors++
			}
		}
		var severityParts []string
		if fileErrors > 0 {
			severityParts = append(severityParts, fmt.Sprintf("%d errors", fileErrors))
		}
		if fileWarnings > 0 {
			severityParts = append(severityParts, fmt.Sprintf("%d warnings", fileWarnings))
		}
		fmt.Fprintf(&body, "\n%s (%s)\n", file, strings.Join(severityParts, ", "))
		body.WriteString(strings.Repeat("-", 40) + "\n")
		for _, e := range errs {
			fmt.Fprintf(&body, "  Line %s:%s [%s] %s\n", e.line, e.column, e.rule, e.message)
		}
	}

	// Full report (legacy content): summary + findings-by-file + raw output.
	var sb strings.Builder
	sb.WriteString(strings.Repeat("=", 80) + "\n")
	fmt.Fprintf(&sb, "LINT REPORT: %s\n", appName)
	fmt.Fprintf(&sb, "Generated: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	sb.WriteString(strings.Repeat("=", 80) + "\n\n")

	fmt.Fprintf(&sb, "Total findings: %d (%d errors, %d warnings)\n", len(realErrors), errorCount, warningCount)
	fmt.Fprintf(&sb, "Total parsed: %d\n", len(allErrors))
	fmt.Fprintf(&sb, "Filtered out: %d\n\n", len(allErrors)-len(realErrors))

	sb.WriteString(strings.Repeat("=", 80) + "\n")
	sb.WriteString("FINDINGS BY FILE\n")
	sb.WriteString(strings.Repeat("=", 80) + "\n\n")
	sb.WriteString(body.String())

	sb.WriteString("\n\n" + strings.Repeat("=", 80) + "\n")
	fmt.Fprintf(&sb, "RAW %s OUTPUT\n", strings.ToUpper(linterName))
	sb.WriteString(strings.Repeat("=", 80) + "\n\n")
	sb.WriteString(rawOutput)

	// Findings-only report: just the surviving findings, grouped by file.
	findings := findingsDoc("LINT", appName, len(realErrors), body.String())

	return writeDualReport(baseDir, "lint", appName, findings, sb.String())
}

// runLinter runs linter ("oxlint" or "eslint") in appPath with args.
func runLinter(linter, appPath string, args ...string) (string, error) {
	if linter == "oxlint" {
		return runOxlint(appPath, args...)
	}
	return runEslint(appPath, args...)
}

// runEslint runs the project's installed ESLint with args (see resolveNodeBin).
// It never reaches for a global or npx-fetched copy; if eslint isn't installed
// it returns an error so the commit fails loudly rather than passing unlinted.
func runEslint(appPath string, args ...string) (string, error) {
	bin, ok := resolveNodeBin(appPath, "eslint")
	if !ok {
		return "", fmt.Errorf("eslint is not installed for %s — run your install and retry", appPath)
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = appPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run() // Ignore exit code, we'll determine success from filtered errors

	return stdout.String(), nil
}

// runOxlint runs the project's installed Oxlint with args (see resolveNodeBin).
// It never falls back to a global or network-fetching runner. If oxlint isn't
// installed it returns an error so the commit fails loudly — a missing linter
// must never pass as a clean lint. Combined stdout+stderr is returned for
// parsing.
func runOxlint(appPath string, args ...string) (string, error) {
	bin, ok := resolveNodeBin(appPath, "oxlint")
	if !ok {
		return "", fmt.Errorf("oxlint is not installed for %s — run your install (e.g. bun install) and retry", appPath)
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = appPath
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run() // Ignore exit code, oxlint returns non-zero when errors found

	return out.String(), nil
}

// parseEslintErrors parses ESLint output into individual errors
// ESLint output format:
//
//	/path/to/file.tsx
//	  10:5  error  'foo' is defined but never used  @typescript-eslint/no-unused-vars
//	  15:10  warning  Unexpected any  @typescript-eslint/no-explicit-any
func parseEslintErrors(output string) []lintError {
	var errors []lintError
	lines := strings.Split(output, "\n")

	// Regex to match error lines: "  10:5  error  message  rule"
	// Format: leading whitespace, line:col, severity, message, rule
	errorLineRe := regexp.MustCompile(`^\s+(\d+):(\d+)\s+(error|warning)\s+(.+?)\s{2,}([@\w/-]+)\s*$`)

	// Regex to match file paths (lines that start with / or drive letter and end with file extension)
	filePathRe := regexp.MustCompile(`^(/[^\s]+|[A-Za-z]:\\[^\s]+)\.(tsx?|jsx?|mjs|cjs)$`)

	currentFile := ""
	for _, line := range lines {
		// Check if this is a file path line
		if filePathRe.MatchString(strings.TrimSpace(line)) {
			currentFile = strings.TrimSpace(line)
			continue
		}

		// Check if this is an error line
		if matches := errorLineRe.FindStringSubmatch(line); matches != nil && currentFile != "" {
			errors = append(errors, lintError{
				filePath: currentFile,
				line:     matches[1],
				column:   matches[2],
				severity: matches[3],
				message:  strings.TrimSpace(matches[4]),
				rule:     matches[5],
				fullText: line,
			})
		}
	}

	return errors
}

// parseOxlintErrors parses Oxlint output into individual errors.
//
// Oxlint emits two different shapes, and this hook has to handle both.
//
// Graphical (miette) format, produced when rendering to a TTY:
//
//	x plugin(rule): message
//	 ,-[filepath:line:col]
//
// or, for warnings:
//
//	! plugin(rule): message
//	 ,-[filepath:line:col]
//
// Compact format, produced when stdout is not a TTY — which is how this hook
// always invokes oxlint, since it captures the output into a buffer:
//
//	filepath:line:col: severity plugin(rule): message help: hint
//
// Only the graphical shape was handled originally, so every finding from a
// buffered run parsed as zero and the lint gate passed unconditionally.
// Callers must treat "output with findings in it, nothing parsed" as a
// failure rather than a clean lint; see runFilteredLintBuffered.
func parseOxlintErrors(output string) []lintError {
	var errors []lintError
	lines := strings.Split(output, "\n")

	// Regex to match error/warning lines: "  x plugin(rule): message" or "  ! plugin(rule): message"
	errorLineRe := regexp.MustCompile(`^\s+([x!])\s+(\S+)\(([^)]+)\):\s+(.+)$`)

	// Regex to match file location: ",-[filepath:line:col]"
	fileLocRe := regexp.MustCompile(`,-\[([^:]+):(\d+):(\d+)\]`)

	// Regex to match a whole compact finding on one line:
	// "filepath:line:col: severity plugin(rule): message".
	// The optional drive-letter prefix keeps Windows absolute paths intact,
	// since the path segment itself may not contain a colon.
	compactRe := regexp.MustCompile(`^((?:[A-Za-z]:)?[^:]+):(\d+):(\d+): (error|warning) ([^\s(]+)\(([^)]+)\): (.+)$`)

	// Oxlint appends " help: ..." to the compact message, whereas the graphical
	// format carries the hint on its own line. Strip it so a finding produces
	// the same message text regardless of which format it was parsed from.
	helpRe := regexp.MustCompile(`\s+help:\s.*$`)

	var currentRule, currentMessage, currentSeverity string
	for _, line := range lines {
		// Compact format carries the whole finding on a single line, so it is
		// complete on its own and needs no location lookahead.
		if match := compactRe.FindStringSubmatch(line); match != nil {
			errors = append(errors, lintError{
				filePath: match[1],
				line:     match[2],
				column:   match[3],
				severity: match[4],
				message:  helpRe.ReplaceAllString(match[7], ""),
				rule:     match[5] + "/" + match[6], // e.g., "react/purity"
				fullText: line,
			})
			continue
		}

		// Check if this is an error/warning line
		if match := errorLineRe.FindStringSubmatch(line); match != nil {
			severity := "error"
			if match[1] == "!" {
				severity = "warning"
			}
			currentSeverity = severity
			currentRule = match[2] + "/" + match[3] // e.g., "eslint/no-unused-vars"
			currentMessage = match[4]
			continue
		}

		// Check if this is a file location line
		if currentRule != "" {
			if match := fileLocRe.FindStringSubmatch(line); match != nil {
				errors = append(errors, lintError{
					filePath: match[1],
					line:     match[2],
					column:   match[3],
					severity: currentSeverity,
					message:  currentMessage,
					rule:     currentRule,
					fullText: line,
				})
				currentRule = ""
			}
		}
	}

	return errors
}

// lintFindingHintRe matches the line:column position that every finding carries
// in each output format parsed here. It is deliberately loose: its only job is
// to answer "did this output contain findings at all", which is what separates
// a genuinely clean run from one whose format the parser no longer understands.
var lintFindingHintRe = regexp.MustCompile(`\d+:\d+`)

// firstNonEmptyLine returns the first non-blank line of s, trimmed, for use in
// diagnostics. It returns "" when s holds no content.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// unparsedLintOutput reports whether a linter emitted findings that the parser
// could not read. Treating such a run as a clean lint is exactly how a linter
// format change silently disables this gate, so callers must fail on it — the
// same principle as runOxlint refusing to pass when the linter is missing.
func unparsedLintOutput(errors []lintError, output string) bool {
	return len(errors) == 0 && lintFindingHintRe.MatchString(output)
}

// errUnparsedLintOutput builds the failure returned for such a run, quoting the
// first line of output so the format change can be diagnosed from the log.
func errUnparsedLintOutput(linter, output string) error {
	return fmt.Errorf(
		"%s produced findings that could not be parsed — its output format has probably changed.\n"+
			"   Update the %s parser in cmd/pre-commit/eslint.go to match. First line of output:\n"+
			"   %s",
		linter, linter, firstNonEmptyLine(output))
}

// shouldFilterLintError checks if a lint error should be filtered out
func shouldFilterLintError(err lintError, rules, excludePaths []string, ignoreWarnings bool) bool {
	// Filter warnings when configured to only fail on errors
	if ignoreWarnings && err.severity == "warning" {
		return true
	}

	// Filter specific rules
	for _, rule := range rules {
		if err.rule == rule {
			return true
		}
	}

	// Filter ALL errors from excluded paths (test files)
	for _, pattern := range excludePaths {
		if strings.Contains(err.filePath, pattern) {
			return true
		}
	}

	return false
}

// convexEslintPaths are the package directories to check for an ESLint config
var convexEslintPaths = []string{"packages/convex", "packages/backend"}

// findConvexEslintPath checks if an ESLint config exists in a convex/backend
// package directory. Returns the path if found, empty string otherwise.
func findConvexEslintPath() string {
	configNames := []string{"eslint.config.mjs", "eslint.config.js", "eslint.config.cjs"}
	for _, dir := range convexEslintPaths {
		for _, cfg := range configNames {
			if _, err := os.Stat(filepath.Join(dir, cfg)); err == nil {
				return dir
			}
		}
	}
	return ""
}

// runConvexEslint runs the project's installed ESLint in a convex/backend
// package directory (no --fix, analysis only). It never reaches for a global or
// npx-fetched copy; when a convex eslint config exists but eslint isn't
// installed it returns an error so the commit fails loudly rather than skipping
// the check.
func runConvexEslint(pkgPath string) (string, error) {
	bin, ok := resolveNodeBin(pkgPath, "eslint")
	if !ok {
		return "", fmt.Errorf("eslint is not installed for %s — run your install and retry", pkgPath)
	}

	cmd := exec.Command(bin, ".")
	cmd.Dir = pkgPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run() // Ignore exit code, we parse output for findings

	return stdout.String(), nil
}

// runConvexEslintBuffered detects and runs ESLint in a convex/backend package,
// returning buffered output suitable for parallel execution.
// Returns empty string and nil error if no ESLint config is found.
func runConvexEslintBuffered(lf LintFilter) (string, error) {
	pkgPath := findConvexEslintPath()
	if pkgPath == "" {
		return "", nil
	}

	appName := filepath.Base(pkgPath) + "-eslint"

	var output strings.Builder
	fmt.Fprintf(&output, "   → Running ESLint for %s...\n", pkgPath)

	lintOutput, err := runConvexEslint(pkgPath)
	if err != nil {
		// runConvexEslint only errors when eslint isn't installed (it ignores
		// eslint's own exit code). A convex eslint config exists, so that's a
		// setup error that must fail the commit — never a silent skip.
		fmt.Fprintf(&output, "   ❌ %v\n", err)
		return output.String(), err
	}

	errors := parseEslintErrors(lintOutput)
	if unparsedLintOutput(errors, lintOutput) {
		err := errUnparsedLintOutput("eslint", lintOutput)
		fmt.Fprintf(&output, "   ❌ %v\n", err)
		return output.String(), err
	}

	excludePaths := lf.ExcludePaths
	if excludePaths == nil {
		excludePaths = DefaultLintExcludePaths
	}

	var realErrors []lintError
	for _, e := range errors {
		if shouldFilterLintError(e, lf.Rules, excludePaths, lf.IgnoreWarnings) {
			continue
		}
		realErrors = append(realErrors, e)
	}

	filteredCount := len(errors) - len(realErrors)
	if filteredCount > 0 {
		fmt.Fprintf(&output, "   (filtered %d convex eslint errors)\n", filteredCount)
	}

	// Write report if reportDir is set
	if reportDir != "" {
		if err := writeLintReport(appName, "eslint", lintOutput, errors, realErrors, reportDir); err != nil {
			fmt.Fprintf(&output, "   Warning: failed to write convex eslint report: %v\n", err)
		}
	}

	if len(realErrors) > 0 {
		output.WriteString("\n")
		currentFile := ""
		for _, e := range realErrors {
			if e.filePath != currentFile {
				if currentFile != "" {
					output.WriteString("\n")
				}
				output.WriteString(e.filePath)
				output.WriteString("\n")
				currentFile = e.filePath
			}
			fmt.Fprintf(&output, "  %s:%s  %s  %s  %s\n", e.line, e.column, e.severity, e.message, e.rule)
		}
		fmt.Fprintf(&output, "   ❌ %s eslint: found %d finding(s)\n", pkgPath, len(realErrors))
		return output.String(), fmt.Errorf("found %d convex eslint finding(s)", len(realErrors))
	}

	fmt.Fprintf(&output, "   ✓ %s eslint passed\n", pkgPath)
	return output.String(), nil
}

// runFilteredLintBuffered lints the whole app at appPath and returns buffered
// output (for parallel execution).
//
// On a commit, the app's fully staged files (stagedFiles, relative to the
// current directory) are first fixed and re-staged so the fixes land in the
// commit; see fixStagedLintFiles. The whole-app pass that decides the result
// never runs --fix, so an autofixable finding anywhere else fails the commit
// and is left on disk for the developer. --all-files makes no commit, so it
// fixes nothing.
func runFilteredLintBuffered(appName, appPath string, stagedFiles []string, lf LintFilter) (string, error) {
	var output strings.Builder

	excludePaths := lf.ExcludePaths
	if excludePaths == nil {
		excludePaths = DefaultLintExcludePaths
	}

	// Determine which linter to use (default to eslint for backwards compatibility)
	linter := lf.Linter
	if linter == "" {
		linter = "eslint"
	}

	if !allFiles {
		fix, err := fixStagedLintFiles(linter, appPath, stagedFiles)
		if err != nil {
			return "", err
		}
		writeLintFixSummary(&output, fix)
	}

	lintOutput, err := runLinter(linter, appPath, ".")
	if err != nil {
		return "", err
	}

	// Parse and filter errors
	var errors []lintError
	if linter == "oxlint" {
		errors = parseOxlintErrors(lintOutput)
	} else {
		errors = parseEslintErrors(lintOutput)
	}

	if unparsedLintOutput(errors, lintOutput) {
		return "", errUnparsedLintOutput(linter, lintOutput)
	}

	var realErrors []lintError
	for _, e := range errors {
		if shouldFilterLintError(e, lf.Rules, excludePaths, lf.IgnoreWarnings) {
			continue
		}
		realErrors = append(realErrors, e)
	}

	// Print filtered count
	filteredCount := len(errors) - len(realErrors)
	if filteredCount > 0 {
		fmt.Fprintf(&output, "   (filtered %d lint errors)\n", filteredCount)
	}

	// Write report if reportDir is set
	if reportDir != "" {
		if err := writeLintReport(appName, linter, lintOutput, errors, realErrors, reportDir); err != nil {
			fmt.Fprintf(&output, "   Warning: failed to write lint report: %v\n", err)
		}
	}

	// Print real errors grouped by file
	if len(realErrors) > 0 {
		output.WriteString("\n")
		currentFile := ""
		for _, e := range realErrors {
			if e.filePath != currentFile {
				if currentFile != "" {
					output.WriteString("\n")
				}
				output.WriteString(e.filePath)
				output.WriteString("\n")
				currentFile = e.filePath
			}
			fmt.Fprintf(&output, "  %s:%s  %s  %s  %s\n", e.line, e.column, e.severity, e.message, e.rule)
		}
		fmt.Fprintf(&output, "\n   The hook only autofixes fully staged files. For the rest, run %s --fix on them and stage the result.\n", linter)
		return output.String(), fmt.Errorf("found %d lint error(s)", len(realErrors))
	}

	return output.String(), nil
}
