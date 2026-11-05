package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFailureDetail(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"reports a lint count", errors.New("found 5 lint error(s)"), "5 errors"},
		{"reports a typecheck count", fmt.Errorf("found 12 typecheck error(s) in web"), "12 errors"},
		// #1290: a check that never started printed "1 errors" and nothing else.
		{
			"shows why a check could not start",
			errors.New("typecheck compiler (tsc/tsgo) is not installed for web — run your install and retry"),
			"typecheck compiler (tsc/tsgo) is not installed for web — run your install and retry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := failureDetail(tt.err); got != tt.want {
				t.Errorf("failureDetail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFinalizePhaseResultsCompactShowsUncountedError(t *testing.T) {
	prevReportDir, prevVerbose := reportDir, verboseFlag
	t.Cleanup(func() { reportDir, verboseFlag = prevReportDir, prevVerbose })
	reportDir, verboseFlag = t.TempDir(), false

	var out bytes.Buffer
	results := []AppCheckResult{
		{AppName: "web", Err: errors.New("oxlint is not installed for apps/web"), Errors: 1},
		{AppName: "admin", Err: errors.New("found 3 lint error(s)"), Errors: 3},
	}
	if err := finalizePhaseResults(&out, "Lint", "lint/", results, 2); err == nil {
		t.Fatal("finalizePhaseResults() = nil, want an error")
	}
	got := out.String()
	for _, want := range []string{"admin 3 errors", "web oxlint is not installed for apps/web"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not contain %q", got, want)
		}
	}
}
