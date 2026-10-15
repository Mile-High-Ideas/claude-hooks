package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DefaultConvexSuccessMarker is the completion line `convex dev` prints once it
// has finished pushing. Only meaningful in ConvexModeDev; codegen prints no
// equivalent.
const DefaultConvexSuccessMarker = "Convex functions ready!"

const (
	// ConvexModeCodegen validates with `convex codegen --typecheck enable`,
	// which the Convex CLI documents as not modifying the code running on the
	// deployment. This is the default.
	//
	// It is also a stricter typecheck than the dev path: `convex dev` defaults
	// `--typecheck` to "try", which silently skips when tsc cannot run.
	ConvexModeCodegen = "codegen"

	// ConvexModeDev validates with `convex dev --once`, which DEPLOYS. Step 2
	// of `convex dev` is documented as "pushes code to the configured dev
	// deployment", and `--once` includes it. With CONVEX_DEPLOY_KEY set to a
	// production deployment, committing becomes a production deploy — see
	// Mile-High-Ideas/claude-hooks#3.
	//
	// Retained for anyone who wants server-side push validation and accepts
	// that cost. Not the default.
	ConvexModeDev = "dev"
)

// resolveConvexMode normalises the configured mode. Anything other than an
// explicit "dev" means codegen, so a typo fails safe rather than deploying.
func resolveConvexMode(mode string) string {
	if mode == ConvexModeDev {
		return ConvexModeDev
	}
	return ConvexModeCodegen
}

// convexCommandArgs returns the CLI arguments for a mode.
func convexCommandArgs(mode string) []string {
	if resolveConvexMode(mode) == ConvexModeDev {
		return []string{"dev", "--once"}
	}
	return []string{"codegen", "--typecheck", "enable"}
}

// evaluateConvexRun decides whether a run passed.
//
// Split out from checkConvex so the decision is testable without shelling out
// to the Convex CLI.
func evaluateConvexRun(mode, marker, output string, cmdErr error) error {
	resolved := resolveConvexMode(mode)

	if cmdErr != nil {
		return fmt.Errorf("convex %s failed: %w\nOutput: %s", resolved, cmdErr, output)
	}

	// codegen prints no completion line, so its exit status is the whole
	// signal. Requiring a marker here would fail every successful run.
	if resolved == ConvexModeCodegen {
		return nil
	}

	if marker == "" {
		marker = DefaultConvexSuccessMarker
	}
	if !strings.Contains(output, marker) {
		return fmt.Errorf("convex validation failed: success marker %q not found in output\nOutput: %s", marker, output)
	}

	return nil
}

// checkConvex validates that Convex functions compile.
func checkConvex(config ConvexConfig) error {
	if config.Path == "" {
		return fmt.Errorf("convex path is required")
	}

	if err := guardConvexProductionDeployment(config, os.LookupEnv); err != nil {
		return err
	}

	mode := resolveConvexMode(config.Mode)

	output, ok, err := runConvex(config.Path, mode)
	if !ok {
		return fmt.Errorf("convex CLI is not installed at %s — run your install and retry", config.Path)
	}

	result := evaluateConvexRun(mode, config.SuccessMarker, output, err)
	_ = writeRunReport("convex-validation", "Convex validation", output, result != nil)

	return result
}

// runConvex runs the Convex CLI installed in the project (see resolveNodeBin),
// never a bunx/npx-fetched one. ok is false when convex isn't installed, so the
// caller fails the commit loudly rather than passing an unvalidated backend.
func runConvex(path, mode string) (string, bool, error) {
	bin, ok := resolveNodeBin(path, "convex")
	if !ok {
		return "", false, nil
	}

	cmd := exec.Command(bin, convexCommandArgs(mode)...)
	cmd.Dir = path

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String() + stderr.String(), true, err
}
