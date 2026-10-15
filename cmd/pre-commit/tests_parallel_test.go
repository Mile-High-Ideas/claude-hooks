package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// threeApps is a test plan whose filters equal the app names, so a fake
// runner can tell which suite it was asked to run from args[1].
func threeApps() map[string]AppConfig {
	return map[string]AppConfig{
		"web":     {Filter: "web"},
		"backend": {Filter: "backend"},
		"mobile":  {Filter: "mobile"},
	}
}

// useTestRunner swaps runCapturedTestCommand and turns on compact mode for
// the duration of one test.
func useTestRunner(t *testing.T, run func(env map[string]string, name string, args ...string) (string, error)) {
	t.Helper()
	prevRun, prevDir, prevVerbose := runCapturedTestCommand, reportDir, verboseFlag
	runCapturedTestCommand = run
	reportDir = t.TempDir()
	verboseFlag = false
	t.Cleanup(func() {
		runCapturedTestCommand, reportDir, verboseFlag = prevRun, prevDir, prevVerbose
	})
}

func testRunContext(parallel bool) TestRunContext {
	runOnShared := true
	return TestRunContext{
		AllApps:        threeApps(),
		GlobalEnabled:  true,
		PackageManager: "bun",
		Config:         TestConfig{Parallel: parallel, RunOnSharedChanges: &runOnShared},
	}
}

func TestRunTestsParallelStartsEverySuiteBeforeAnyFinishes(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	useTestRunner(t, func(_ map[string]string, _ string, args ...string) (string, error) {
		started <- args[1]
		<-release
		return "Tests  10 passed (10)", nil
	})

	done := make(chan error, 1)
	go func() { done <- runTests(testRunContext(true)) }()

	// Every suite must be running at once. A sequential runner blocks on
	// the first suite and never starts the second, so this times out.
	seen := map[string]bool{}
	for len(seen) < 3 {
		select {
		case app := <-started:
			seen[app] = true
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatalf("only %d of 3 suites started concurrently: %v", len(seen), seen)
		}
	}
	close(release)

	if err := <-done; err != nil {
		t.Fatalf("runTests() = %v, want nil", err)
	}
}

func TestRunTestsSequentialRunsSuitesInAppNameOrder(t *testing.T) {
	var order []string
	useTestRunner(t, func(_ map[string]string, _ string, args ...string) (string, error) {
		order = append(order, args[1])
		return "Tests  1 passed (1)", nil
	})

	if err := runTests(testRunContext(false)); err != nil {
		t.Fatalf("runTests() = %v, want nil", err)
	}
	want := []string{"backend", "mobile", "web"}
	if len(order) != len(want) {
		t.Fatalf("ran %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ran %v, want %v (sorted by app name)", order, want)
		}
	}
}

func TestRunTestsParallelNamesEveryFailedAppInOrder(t *testing.T) {
	useTestRunner(t, func(_ map[string]string, _ string, args ...string) (string, error) {
		if args[1] == "backend" {
			return "Tests  2 passed (2)", nil
		}
		return "Tests  3 failed | 7 passed (10)", errors.New("exit status 1")
	})

	err := runTests(testRunContext(true))
	if err == nil {
		t.Fatal("runTests() = nil, want an error naming the failed apps")
	}
	if got, want := err.Error(), "mobile, web tests failed"; got != want {
		t.Errorf("runTests() error = %q, want %q", got, want)
	}
}

func TestRunTestsParallelRetriesOnlyTheFailingApp(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	useTestRunner(t, func(_ map[string]string, _ string, args ...string) (string, error) {
		mu.Lock()
		calls[args[1]]++
		n := calls[args[1]]
		mu.Unlock()
		if args[1] == "web" && n == 1 {
			return "Tests  1 failed | 9 passed (10)", errors.New("exit status 1")
		}
		return "Tests  10 passed (10)", nil
	})

	ctx := testRunContext(true)
	ctx.Config.Retries = 1
	if err := runTests(ctx); err != nil {
		t.Fatalf("runTests() = %v, want nil after web recovers on retry", err)
	}
	want := map[string]int{"web": 2, "backend": 1, "mobile": 1}
	for app, n := range want {
		if calls[app] != n {
			t.Errorf("%s ran %d times, want %d", app, calls[app], n)
		}
	}
}
