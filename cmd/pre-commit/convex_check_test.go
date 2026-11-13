package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvexFilesInScope(t *testing.T) {
	cfg := ConvexCheckConfig{
		AppPaths:     []string{"packages/backend/convex"},
		ExcludePaths: []string{"_generated/", "schema/"},
	}
	root := "/repo"
	staged := []string{
		"/repo/packages/backend/convex/events/eventsQueries.ts",
		"/repo/packages/backend/convex/_generated/api.d.ts", // excluded
		"/repo/packages/backend/convex/schema/events.ts",    // excluded
		"/repo/apps/story/foo.ts",                           // not in appPaths
		"/repo/packages/backend/convex/x.test.ts",           // test skip
	}
	got := convexFilesInScope(cfg, root, staged)
	if len(got) != 1 || got[0] != "/repo/packages/backend/convex/events/eventsQueries.ts" {
		t.Fatalf("unexpected scope result: %#v", got)
	}
}

func TestConvexCheckNoopWhenDormant(t *testing.T) {
	// no .convex-lint.json under projectRoot => empty config => dormant (nil)
	if err := runConvexCheck("/nonexistent-root", nil); err != nil {
		t.Fatalf("expected nil when dormant, got %v", err)
	}
}

func TestFormatCommitViolations(t *testing.T) {
	raw := `{"diagnostics":[
		{"code":"convex(type-exports-location)","message":"move types","filename":"/r/convex/a.ts","labels":[{"span":{"line":3}}]},
		{"code":"@convex-dev(explicit-table-ids)","message":"add table","filename":"/r/convex/b.ts","labels":[{"span":{"line":7}}]},
		{"code":"convex(file-size)","message":"too big","filename":"/r/convex/c.ts","labels":[]},
		{"code":"@convex-dev(require-args-validator)","message":"no args","filename":"/r/convex/d.ts","labels":[{"span":{"line":1}}]},
		{"code":"eslint(no-unused-vars)","message":"unused","filename":"/r/convex/e.ts","labels":[{"span":{"line":2}}]}
	]}`
	var res oxlintResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"type-exports-location":          true,
		"@convex-dev/explicit-table-ids": true,
		"file-size":                      true,
	}
	got := formatCommitViolations(res, want)
	expected := []string{
		"  a.ts:3  type-exports-location — move types",
		"  b.ts:7  @convex-dev/explicit-table-ids — add table",
		"  c.ts  file-size — too big",
	}
	if strings.Join(got, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("formatCommitViolations =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(expected, "\n"))
	}
}

func TestLoadConvexConfigReadsLegacyEslintKey(t *testing.T) {
	root := t.TempDir()
	cfgJSON := `{
		// comment
		"errorRules": ["@convex-dev/explicit-table-ids"],
		"eslintErrorRules": ["require-args-validator"]
	}`
	if err := os.WriteFile(filepath.Join(root, ".convex-lint.json"), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConvexConfig(root)
	if len(cfg.ErrorRules) != 1 || cfg.ErrorRules[0] != "@convex-dev/explicit-table-ids" {
		t.Errorf("ErrorRules = %#v", cfg.ErrorRules)
	}
	if len(cfg.LegacyEslintErrorRules) != 1 || cfg.LegacyEslintErrorRules[0] != "require-args-validator" {
		t.Errorf("LegacyEslintErrorRules = %#v", cfg.LegacyEslintErrorRules)
	}
}
