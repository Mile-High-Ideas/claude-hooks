package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/milehighideas/claude-hooks/internal/convexrules"
	"github.com/milehighideas/claude-hooks/internal/jsonc"
)

const convexTestSuffixSkip = ".test."

// loadConvexConfig reads .convex-lint.json from the project root — the single
// source of truth shared with the oxlint plugin. Missing/invalid file = empty
// config (feature dormant).
func loadConvexConfig(projectRoot string) ConvexCheckConfig {
	var cfg ConvexCheckConfig
	_ = jsonc.Unmarshal(filepath.Join(projectRoot, ".convex-lint.json"), &cfg)
	return cfg
}

// convexFilesInScope filters staged absolute paths to in-scope Convex files.
func convexFilesInScope(cfg ConvexCheckConfig, projectRoot string, staged []string) []string {
	var out []string
	for _, f := range staged {
		rel := strings.ReplaceAll(f, "\\", "/")
		if strings.Contains(rel, "/_generated/") || strings.Contains(rel, convexTestSuffixSkip) {
			continue
		}
		excluded := false
		for _, ex := range cfg.ExcludePaths {
			if ex != "" && strings.Contains(rel, ex) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		inApp := len(cfg.AppPaths) == 0
		for _, ap := range cfg.AppPaths {
			if ap != "" && strings.Contains(rel, ap) {
				inApp = true
				break
			}
		}
		if inApp && (strings.HasSuffix(f, ".ts") || strings.HasSuffix(f, ".tsx")) {
			out = append(out, f)
		}
	}
	return out
}

type oxlintResult struct {
	Diagnostics []struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Filename string `json:"filename"`
		Labels   []struct {
			Span struct {
				Line int `json:"line"`
			} `json:"span"`
		} `json:"labels"`
	} `json:"diagnostics"`
}

// oxlintCommitViolations runs oxlint on the staged files and returns formatted
// messages for Convex diagnostics (convex(*) and @convex-dev(*)) whose
// errorRules id is in want. oxlint emits every Convex rule as a warning (from
// .oxlintrc); we gate on errorRules membership, not oxlint severity (oxlint -D
// does not override a JS-plugin rule's config severity).
func oxlintCommitViolations(projectRoot string, files []string, want map[string]bool) []string {
	// Use the project's installed oxlint (see resolveNodeBin) so this gate runs
	// the same version as the app lint pass. A missing install degrades to the
	// unmarshal-failure path below (non-blocking), same as before.
	oxlintBin, _ := resolveNodeBin(projectRoot, "oxlint")
	args := append([]string{"--format=json"}, files...)
	cmd := exec.Command(oxlintBin, args...)
	cmd.Dir = projectRoot
	out, _ := cmd.Output() // non-zero exit when findings exist; parse stdout regardless

	var res oxlintResult
	if json.Unmarshal(out, &res) != nil {
		return nil // oxlint not available / parse failure → don't wedge the commit
	}
	return formatCommitViolations(res, want)
}

// formatCommitViolations keeps the diagnostics whose errorRules id is in want,
// formatted as "  file:line  rule — message".
func formatCommitViolations(res oxlintResult, want map[string]bool) []string {
	var msgs []string
	for _, d := range res.Diagnostics {
		rule := convexrules.RuleKey(d.Code)
		if rule == "" || !want[rule] {
			continue
		}
		loc := filepath.Base(d.Filename)
		if len(d.Labels) > 0 && d.Labels[0].Span.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, d.Labels[0].Span.Line)
		}
		msgs = append(msgs, fmt.Sprintf("  %s  %s — %s", loc, rule, convexrules.CleanMessage(d.Message)))
	}
	return msgs
}

// runConvexCheck loads .convex-lint.json and blocks when a staged convex file
// trips a rule listed in errorRules. Mirrors the validate-convex edit-time hook
// so commit and edit enforce the same set. No-op when errorRules is empty.
func runConvexCheck(projectRoot string, stagedAbs []string) error {
	cfg := loadConvexConfig(projectRoot)
	if len(cfg.LegacyEslintErrorRules) > 0 {
		printWarningStatus("Convex check", convexrules.LegacyEslintKeyWarning)
	}
	want := map[string]bool{}
	for _, r := range cfg.ErrorRules {
		want[r] = true
	}
	if len(want) == 0 {
		return nil // dormant
	}
	files := convexFilesInScope(cfg, projectRoot, stagedAbs)
	if len(files) == 0 {
		return nil
	}

	diags := oxlintCommitViolations(projectRoot, files, want)
	failed := len(diags) > 0
	_ = writeRunReport("convex-check", "Convex check", strings.Join(diags, "\n"), failed)
	if failed {
		printStatus("Convex check", false, fmt.Sprintf("%d violation(s)", len(diags)))
		printReportHint("convex-check/")
		return fmt.Errorf("convexCheck: %d violation(s)\n%s", len(diags), strings.Join(diags, "\n"))
	}
	printStatus("Convex check", true, fmt.Sprintf("%d file(s)", len(files)))
	return nil
}
