// Command validate-convex is a PreToolUse hook (Write|Edit) that lints the edited
// Convex file with oxlint and blocks (exit 2) on the rules listed in
// .convex-lint.json errorRules: bare ids for @milehighideas/oxlint-plugin-convex
// rules, "@convex-dev/<rule>" for @convex-dev/eslint-plugin rules loaded through
// oxlint's jsPlugins. Gated on rule-id membership (oxlint -D does not override a
// JS-plugin rule's config severity). Empty errorRules = dormant.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/milehighideas/claude-hooks/internal/convexrules"
	"github.com/milehighideas/claude-hooks/internal/jsonc"
)

type hookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath   string `json:"file_path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
		Content    string `json:"content"`
	} `json:"tool_input"`
}

type convexCfg struct {
	AppPaths     []string `json:"appPaths"`
	ExcludePaths []string `json:"excludePaths"`
	// ErrorRules: Convex rule ids enforced (blocking) at edit time, bare for
	// oxlint-plugin-convex and "@convex-dev/<rule>" for @convex-dev/eslint-plugin.
	// Empty = dormant. .oxlintrc.json stays at "warn"; this is the ratchet.
	ErrorRules []string `json:"errorRules"`
	// LegacyEslintErrorRules is the old eslintErrorRules key, decoded only to
	// warn that it no longer has any effect.
	LegacyEslintErrorRules []string `json:"eslintErrorRules"`
}

func isConvexTarget(path string, appPaths, exclude []string) bool {
	f := strings.ReplaceAll(path, "\\", "/")
	if strings.Contains(f, "/_generated/") || strings.Contains(f, ".test.") || strings.Contains(f, ".spec.") {
		return false
	}
	if !strings.HasSuffix(f, ".ts") && !strings.HasSuffix(f, ".tsx") {
		return false
	}
	for _, ex := range exclude {
		if ex != "" && strings.Contains(f, ex) {
			return false
		}
	}
	if len(appPaths) == 0 {
		return false
	}
	for _, ap := range appPaths {
		if ap != "" && strings.Contains(f, ap) {
			return true
		}
	}
	return false
}

func toSet(xs []string) map[string]bool {
	s := map[string]bool{}
	for _, x := range xs {
		if x != "" {
			s[x] = true
		}
	}
	return s
}

type oxlintResult struct {
	Diagnostics []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"diagnostics"`
}

// oxlintViolations runs oxlint on path and returns formatted messages for any
// Convex diagnostic (convex(*) or @convex-dev(*)) whose errorRules id is in want. It uses the project-pinned
// oxlint (node_modules/.bin/oxlint) so the edit-time hook matches the version
// the commit gate and app lint use. Best effort: if it isn't installed (fresh
// clone, mid-install), returns nil so a transient state never blocks an edit.
func oxlintViolations(projectRoot, path string, want map[string]bool) []string {
	bin := filepath.Join(projectRoot, "node_modules", ".bin", "oxlint")
	if _, err := os.Stat(bin); err != nil {
		return nil // oxlint not installed — skip
	}
	cmd := exec.Command(bin, "--format=json", path)
	cmd.Dir = projectRoot
	out, _ := cmd.Output()
	var res oxlintResult
	if json.Unmarshal(out, &res) != nil {
		return nil
	}
	var msgs []string
	for _, d := range res.Diagnostics {
		if rule := convexrules.RuleKey(d.Code); rule != "" && want[rule] {
			msgs = append(msgs, fmt.Sprintf("  ✗ %s — %s", rule, convexrules.CleanMessage(d.Message)))
		}
	}
	return msgs
}

// projectedContent returns the file's content as it WILL be after this tool
// call, so the ratchet evaluates the PROPOSED result instead of the on-disk
// file. Without this, a file that already has a violation can never be fixed
// through Edit — every edit is blocked because the pre-edit file still has it.
// Returns ("", false) when projection isn't possible (caller lints on-disk).
func projectedContent(in hookInput) (string, bool) {
	switch in.ToolName {
	case "Write":
		if in.ToolInput.Content == "" {
			return "", false
		}
		return in.ToolInput.Content, true
	case "Edit":
		raw, err := os.ReadFile(in.ToolInput.FilePath)
		if err != nil {
			return "", false
		}
		src := string(raw)
		old := in.ToolInput.OldString
		if old == "" || !strings.Contains(src, old) {
			return "", false // let the Edit tool surface a no-match error itself
		}
		if in.ToolInput.ReplaceAll {
			return strings.ReplaceAll(src, old, in.ToolInput.NewString), true
		}
		return strings.Replace(src, old, in.ToolInput.NewString, 1), true
	}
	return "", false
}

// lintProjected writes content to a temp file that preserves the original
// basename (so *.types.ts type-home detection and path-based excludes still
// resolve) inside a dot-prefixed subdir of the original file's directory (so
// oxlint config discovery walks up to the same .oxlintrc.json / tsconfig, and
// Convex's dev watcher — which ignores dotdirs — skips it), then runs the same
// oxlint pass against it. Cleans up explicitly (defers don't run on
// os.Exit). Returns (msgs, true) on success; (nil, false) if the temp couldn't
// be set up, so the caller falls back to the on-disk lint.
func lintProjected(projectRoot, origPath, content string, want map[string]bool) ([]string, bool) {
	dir := filepath.Dir(origPath)
	tmpDir, err := os.MkdirTemp(dir, ".convexlint-tmp-")
	if err != nil {
		return nil, false
	}
	tmpPath := filepath.Join(tmpDir, filepath.Base(origPath))
	if err := os.WriteFile(tmpPath, []byte(content), 0o600); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, false
	}
	msgs := oxlintViolations(projectRoot, tmpPath, want)
	_ = os.RemoveAll(tmpDir)
	return msgs, true
}

func main() {
	data, _ := io.ReadAll(os.Stdin)
	var in hookInput
	if json.Unmarshal(data, &in) != nil {
		os.Exit(0)
	}
	if in.ToolName != "Edit" && in.ToolName != "Write" {
		os.Exit(0)
	}
	path := in.ToolInput.FilePath
	if path == "" {
		os.Exit(0)
	}

	// .convex-lint.json is the single source of truth, shared with the oxlint
	// plugin and the convexCheck commit gate. Read from CWD (the hook runs in
	// the project root). If it can't be read, stay silent (allow).
	var cfg convexCfg
	if err := jsonc.Unmarshal(".convex-lint.json", &cfg); err != nil {
		os.Exit(0)
	}
	if !isConvexTarget(path, cfg.AppPaths, cfg.ExcludePaths) {
		os.Exit(0)
	}

	if len(cfg.LegacyEslintErrorRules) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  %s\n", convexrules.LegacyEslintKeyWarning)
	}
	want := toSet(cfg.ErrorRules)
	if len(want) == 0 {
		os.Exit(0) // dormant
	}

	cwd, _ := os.Getwd()
	// Evaluate the PROJECTED edit result so a file with an existing violation
	// can actually be fixed through Edit; fall back to the on-disk file when the
	// edit can't be projected (e.g. old_string not found, unreadable file).
	var msgs []string
	if content, ok := projectedContent(in); ok {
		if m, done := lintProjected(cwd, path, content, want); done {
			msgs = m
		} else {
			msgs = oxlintViolations(cwd, path, want)
		}
	} else {
		msgs = oxlintViolations(cwd, path, want)
	}

	if len(msgs) > 0 {
		fmt.Fprintf(os.Stderr, "\n❌ BLOCKED: Convex lint violation in %s\n%s\n\nFix the issue (split the file, add returns:, use db.get(\"table\", id), etc.) — see convex/REFACTORING.md.\n",
			filepath.Base(path), strings.Join(msgs, "\n"))
		os.Exit(2)
	}
	os.Exit(0)
}
