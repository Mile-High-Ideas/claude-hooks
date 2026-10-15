package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fake values only. They exist so the tests can assert that no value ever
// reaches an error message.
const (
	fakeProdDeployKey  = "prod:fake-deployment-123|fake-secret-abc"
	fakeProdDeployment = "prod:fake-deployment-123"
	fakeDevDeployKey   = "dev:fake-dev-456|fake-dev-secret"
)

func envFrom(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func writeEnvFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func boolPtr(value bool) *bool { return &value }

func TestIsConvexProductionValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "production deploy key", value: fakeProdDeployKey, want: true},
		{name: "production deployment", value: fakeProdDeployment, want: true},
		{name: "leading whitespace", value: "  " + fakeProdDeployment, want: true},
		{name: "dev deploy key", value: fakeDevDeployKey, want: false},
		{name: "dev deployment", value: "dev:fake-dev-456", want: false},
		{name: "anonymous local deployment", value: "anonymous:anonymous-backend", want: false},
		{name: "local deployment", value: "local:local-backend", want: false},
		{name: "preview deploy key", value: "preview:team:project|secret", want: false},
		{name: "empty", value: "", want: false},
		{name: "prod without colon is not a prefix match", value: "production-thing", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isConvexProductionValue(tt.value); got != tt.want {
				t.Errorf("isConvexProductionValue(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseDotenvLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{name: "plain", line: "CONVEX_DEPLOYMENT=dev:x", wantKey: "CONVEX_DEPLOYMENT", wantValue: "dev:x", wantOK: true},
		{name: "export prefix", line: "export CONVEX_DEPLOYMENT=prod:x", wantKey: "CONVEX_DEPLOYMENT", wantValue: "prod:x", wantOK: true},
		{name: "double quoted", line: `CONVEX_DEPLOY_KEY="prod:x|y"`, wantKey: "CONVEX_DEPLOY_KEY", wantValue: "prod:x|y", wantOK: true},
		{name: "single quoted", line: `CONVEX_DEPLOY_KEY='prod:x|y'`, wantKey: "CONVEX_DEPLOY_KEY", wantValue: "prod:x|y", wantOK: true},
		{name: "spaces around equals", line: "CONVEX_DEPLOYMENT = prod:x", wantKey: "CONVEX_DEPLOYMENT", wantValue: "prod:x", wantOK: true},
		{name: "comment", line: "# CONVEX_DEPLOYMENT=prod:x", wantOK: false},
		{name: "blank", line: "   ", wantOK: false},
		{name: "no equals", line: "CONVEX_DEPLOYMENT", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, value, ok := parseDotenvLine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("parseDotenvLine(%q) ok = %v, want %v", tt.line, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if key != tt.wantKey || value != tt.wantValue {
				t.Errorf("parseDotenvLine(%q) = (%q, %q), want (%q, %q)", tt.line, key, value, tt.wantKey, tt.wantValue)
			}
		})
	}
}

func TestFindConvexProductionDeployment(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		files        map[string]string
		wantVariable string
		wantOrigin   string
	}{
		{
			name:  "nothing configured",
			env:   map[string]string{},
			files: map[string]string{},
		},
		{
			name:         "production deploy key in .env.local",
			files:        map[string]string{".env.local": "CONVEX_DEPLOYMENT=prod:fake\nCONVEX_DEPLOY_KEY=" + fakeProdDeployKey + "\n"},
			wantVariable: "CONVEX_DEPLOY_KEY",
			wantOrigin:   ".env.local",
		},
		{
			name:         "production deployment in .env.local without a key",
			files:        map[string]string{".env.local": "# comment\nCONVEX_DEPLOYMENT=" + fakeProdDeployment + "\n"},
			wantVariable: "CONVEX_DEPLOYMENT",
			wantOrigin:   ".env.local",
		},
		{
			name:         "production deploy key in .env",
			files:        map[string]string{".env": "export CONVEX_DEPLOY_KEY=\"" + fakeProdDeployKey + "\"\n"},
			wantVariable: "CONVEX_DEPLOY_KEY",
			wantOrigin:   ".env",
		},
		{
			name:         "production deploy key in the process environment",
			env:          map[string]string{"CONVEX_DEPLOY_KEY": fakeProdDeployKey},
			files:        map[string]string{".env.local": "CONVEX_DEPLOYMENT=anonymous:anonymous-backend\n"},
			wantVariable: "CONVEX_DEPLOY_KEY",
			wantOrigin:   "the process environment",
		},
		{
			name:  "local anonymous deployment passes",
			files: map[string]string{".env.local": "CONVEX_DEPLOYMENT=anonymous:anonymous-backend\nCONVEX_URL=http://127.0.0.1:3210\n"},
		},
		{
			name:  "dev deploy key passes",
			env:   map[string]string{"CONVEX_DEPLOY_KEY": fakeDevDeployKey},
			files: map[string]string{".env.local": "CONVEX_DEPLOYMENT=dev:fake-dev-456\n"},
		},
		{
			name:  "commented-out production key passes",
			files: map[string]string{".env.local": "# CONVEX_DEPLOY_KEY=" + fakeProdDeployKey + "\nCONVEX_DEPLOYMENT=anonymous:x\n"},
		},
		{
			name:  "unrelated variable with a prod prefix passes",
			files: map[string]string{".env.local": "OTHER_KEY=prod:something\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeEnvFile(t, dir, name, content)
			}

			got, err := findConvexProductionDeployment(dir, envFrom(tt.env))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantVariable == "" {
				if got != nil {
					t.Fatalf("expected no production deployment, got %+v", *got)
				}
				return
			}

			if got == nil {
				t.Fatalf("expected %s from %s, got nil", tt.wantVariable, tt.wantOrigin)
			}
			if got.Variable != tt.wantVariable {
				t.Errorf("variable = %q, want %q", got.Variable, tt.wantVariable)
			}
			if !strings.HasSuffix(got.Origin, tt.wantOrigin) {
				t.Errorf("origin = %q, want suffix %q", got.Origin, tt.wantOrigin)
			}
		})
	}
}

func TestGuardConvexProductionDeploymentRefusesWithoutLeakingValues(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.local", "CONVEX_DEPLOYMENT="+fakeProdDeployment+"\nCONVEX_DEPLOY_KEY="+fakeProdDeployKey+"\n")

	err := guardConvexProductionDeployment(ConvexConfig{Path: dir}, envFrom(nil))
	if err == nil {
		t.Fatal("expected the guard to refuse a production deployment")
	}

	message := err.Error()
	for _, want := range []string{"CONVEX_DEPLOY_KEY", ".env.local", "just convex-local-setup", "refuseProductionDeployment"} {
		if !strings.Contains(message, want) {
			t.Errorf("message should mention %q, got:\n%s", want, message)
		}
	}
	for _, secret := range []string{"fake-deployment-123", "fake-secret-abc"} {
		if strings.Contains(message, secret) {
			t.Errorf("message leaked %q:\n%s", secret, message)
		}
	}
}

func TestGuardConvexProductionDeploymentConfig(t *testing.T) {
	tests := []struct {
		name      string
		setting   *bool
		wantBlock bool
	}{
		{name: "unset defaults to refusing", setting: nil, wantBlock: true},
		{name: "explicit true refuses", setting: boolPtr(true), wantBlock: true},
		{name: "explicit false allows", setting: boolPtr(false), wantBlock: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeEnvFile(t, dir, ".env.local", "CONVEX_DEPLOY_KEY="+fakeProdDeployKey+"\n")

			config := ConvexConfig{Path: dir, RefuseProductionDeployment: tt.setting}
			err := guardConvexProductionDeployment(config, envFrom(nil))

			if tt.wantBlock && err == nil {
				t.Error("expected the guard to refuse")
			}
			if !tt.wantBlock && err != nil {
				t.Errorf("expected the guard to allow, got: %v", err)
			}
		})
	}
}

func TestCheckConvexRefusesProductionBeforeRunningTheCLI(t *testing.T) {
	// No node_modules/.bin/convex exists here, so if the guard did not run
	// first the error would be "convex CLI is not installed".
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env.local", "CONVEX_DEPLOY_KEY="+fakeProdDeployKey+"\n")

	err := checkConvex(ConvexConfig{Path: dir})
	if err == nil {
		t.Fatal("expected checkConvex to refuse a production deployment")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("expected the production refusal, got: %v", err)
	}
}

func TestConvexConfigRefuseProductionDeploymentJSON(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(origDir) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	writeEnvFile(t, dir, ".pre-commit.json", `{
		// JSONC is accepted
		"convex": { "path": "packages/backend", "refuseProductionDeployment": false }
	}`)

	config, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if config.Convex.RefuseProductionDeployment == nil || *config.Convex.RefuseProductionDeployment {
		t.Errorf("expected refuseProductionDeployment=false to be loaded, got %v", config.Convex.RefuseProductionDeployment)
	}
	if convexProductionGuardEnabled(config.Convex) {
		t.Error("guard should be disabled by explicit false")
	}
	if !convexProductionGuardEnabled(ConvexConfig{}) {
		t.Error("guard should be enabled when unset")
	}
}
