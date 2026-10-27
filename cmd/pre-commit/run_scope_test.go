package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplitList(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{in: "", want: nil},
		{in: "web", want: []string{"web"}},
		{in: "lint, typecheck,,tests ", want: []string{"lint", "typecheck", "tests"}},
	}
	for _, tt := range tests {
		if got := splitList(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitList(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestRestrictApps(t *testing.T) {
	newConfig := func() *Config {
		return &Config{Apps: map[string]AppConfig{
			"web":     {Path: "apps/web"},
			"backend": {Path: "packages/backend"},
			"mobile":  {Path: "apps/mobile"},
		}}
	}
	tests := []struct {
		name     string
		apps     []string
		wantApps []string
		wantErr  string
	}{
		{name: "no filter keeps every app", apps: nil, wantApps: []string{"backend", "mobile", "web"}},
		{name: "one app", apps: []string{"web"}, wantApps: []string{"web"}},
		{name: "two apps", apps: []string{"web", "backend"}, wantApps: []string{"backend", "web"}},
		{name: "unknown app is an error", apps: []string{"wbe"}, wantErr: "--app wbe: no such app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newConfig()
			err := restrictApps(config, tt.apps)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := sortedKeys(config.Apps); !reflect.DeepEqual(got, tt.wantApps) {
				t.Errorf("apps = %v, want %v", got, tt.wantApps)
			}
		})
	}
}

func TestSkipFeatures(t *testing.T) {
	features := Features{Lint: true, Typecheck: true, Tests: true, ConvexValidation: true, MockCheck: true}

	if err := skipFeatures(&features, []string{"lint", "tests", "convexValidation"}); err != nil {
		t.Fatal(err)
	}
	if features.Lint || features.Tests || features.ConvexValidation {
		t.Errorf("skipped features still on: lint=%v tests=%v convexValidation=%v", features.Lint, features.Tests, features.ConvexValidation)
	}
	if !features.Typecheck || !features.MockCheck {
		t.Errorf("unskipped features turned off: typecheck=%v mockCheck=%v", features.Typecheck, features.MockCheck)
	}

	err := skipFeatures(&features, []string{"lnit"})
	if err == nil || !strings.Contains(err.Error(), "--skip lnit: no such feature") {
		t.Errorf("unknown feature: err = %v", err)
	}
}

// A per-app `enabled: true` override turns tests on even when features.tests
// is off, so --skip tests has to clear those overrides too. Without that, a
// repo with overrides ran every test suite in the slice meant to skip them.
func TestApplyRunScopeSkipTestsClearsEnabledOverrides(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name        string
		skip        string
		overrides   map[string]AppTestOverride
		wantEnabled map[string]*bool
	}{
		{
			name:        "skip tests clears enabled overrides",
			skip:        "tests",
			overrides:   map[string]AppTestOverride{"web": {Enabled: &on}, "mobile": {Enabled: &off}},
			wantEnabled: map[string]*bool{"web": nil, "mobile": nil},
		},
		{
			name:        "skipping another feature keeps overrides",
			skip:        "lint",
			overrides:   map[string]AppTestOverride{"web": {Enabled: &on}},
			wantEnabled: map[string]*bool{"web": &on},
		},
		{
			name:        "no skip keeps overrides",
			skip:        "",
			overrides:   map[string]AppTestOverride{"web": {Enabled: &on}},
			wantEnabled: map[string]*bool{"web": &on},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				Features:   Features{Tests: true, Lint: true},
				TestConfig: TestConfig{AppOverrides: tt.overrides},
			}
			if err := applyRunScope(config, "", tt.skip); err != nil {
				t.Fatal(err)
			}
			for app, want := range tt.wantEnabled {
				got := config.TestConfig.AppOverrides[app].Enabled
				if (got == nil) != (want == nil) || (got != nil && *got != *want) {
					t.Errorf("%s: enabled = %v, want %v", app, ptrString(got), ptrString(want))
				}
			}
		})
	}
}

// The gate in run() and the per-app filter in the test runner both read the
// overrides; after --skip tests neither may select an app.
func TestSkipTestsLeavesNoAppEnabled(t *testing.T) {
	on := true
	config := &Config{
		Features:   Features{Tests: true},
		TestConfig: TestConfig{AppOverrides: map[string]AppTestOverride{"web": {Enabled: &on}, "backend": {Enabled: &on}}},
	}
	if err := applyRunScope(config, "", "tests"); err != nil {
		t.Fatal(err)
	}
	if testsRequested(config) {
		t.Error("testsRequested = true after --skip tests, want false")
	}
}

func ptrString(b *bool) string {
	if b == nil {
		return "nil"
	}
	if *b {
		return "true"
	}
	return "false"
}

// The scope has to be applied to the config run() hands the checks, not to a
// copy: --skip changelog must let a staged change with no fragment through,
// and an unknown --app must stop the run.
func TestRunAppliesRunScope(t *testing.T) {
	dir := newAllFilesRepo(t)
	config := `{"features": {"changelog": true}, "apps": {"web": {"path": "apps/web"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".pre-commit.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKIP_CHANGELOG_CHECK", "")
	setScope := func(apps, skip string) {
		appFilter, skipFilter = apps, skip
	}
	t.Cleanup(func() { setScope("", "") })

	setScope("", "")
	if err := run(); err == nil || !strings.Contains(err.Error(), "changelog fragment required") {
		t.Fatalf("no scope: err = %v, want the changelog error", err)
	}

	setScope("", "changelog")
	if err := run(); err != nil {
		t.Errorf("--skip changelog: err = %v, want nil", err)
	}

	setScope("mobile", "changelog")
	if err := run(); err == nil || !strings.Contains(err.Error(), "--app mobile: no such app") {
		t.Errorf("--app mobile: err = %v, want the unknown-app error", err)
	}
}
