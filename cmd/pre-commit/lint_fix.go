package main

import (
	"fmt"
	"io"
	"path/filepath"
)

// lintFixBatchSize caps the files passed to one linter run, keeping the command
// line under Windows' 32K character limit on large commits.
const lintFixBatchSize = 100

// lintFixResult records what fixStagedLintFiles did to an app's staged files.
type lintFixResult struct {
	// restaged are the files the linter changed and the hook staged again.
	restaged []string
	// partial are staged files with unstaged changes too. They are not fixed,
	// because staging the fix would pull their unstaged hunks into the commit.
	partial []string
}

// fixStagedLintFiles runs the linter with --fix on the app's staged lintable
// files and stages the ones it changed, so the fixes land in the commit.
//
// Only fully staged files are touched: their working copy equals the index, so
// staging the fixed file stages nothing but the fix. A partially staged file is
// left as it is and reported in partial; the whole-app pass then reports its
// findings. stagedFiles and the returned paths are relative to the current
// directory; appPath is too.
func fixStagedLintFiles(linter, appPath string, stagedFiles []string) (lintFixResult, error) {
	var result lintFixResult
	targets := filterLintableFiles(stagedFiles)
	if len(targets) == 0 {
		return result, nil
	}

	unstaged, err := getUnstagedFiles()
	if err != nil {
		return result, err
	}
	var fix []string
	for _, f := range targets {
		if unstaged[f] {
			result.partial = append(result.partial, f)
			continue
		}
		fix = append(fix, f)
	}
	if len(fix) == 0 {
		return result, nil
	}

	if err := runLinterFix(linter, appPath, fix); err != nil {
		return result, err
	}

	changed, err := getUnstagedFiles()
	if err != nil {
		return result, err
	}
	for _, f := range fix {
		if changed[f] {
			result.restaged = append(result.restaged, f)
		}
	}
	if err := stageFiles(result.restaged); err != nil {
		return result, fmt.Errorf("stage lint fixes: %w", err)
	}
	return result, nil
}

// runLinterFix runs the linter with --fix on files, in batches. The output is
// discarded: the whole-app pass that follows reports whatever is left.
func runLinterFix(linter, appPath string, files []string) error {
	for start := 0; start < len(files); start += lintFixBatchSize {
		end := min(start+lintFixBatchSize, len(files))
		args := []string{"--fix"}
		for _, f := range files[start:end] {
			rel, err := filepath.Rel(appPath, f)
			if err != nil {
				return fmt.Errorf("lint fix path %s: %w", f, err)
			}
			args = append(args, rel)
		}
		if _, err := runLinter(linter, appPath, args...); err != nil {
			return err
		}
	}
	return nil
}

// writeLintFixSummary reports the files the hook fixed and re-staged, and the
// partially staged files it left alone.
func writeLintFixSummary(w io.Writer, fix lintFixResult) {
	for _, f := range fix.restaged {
		_, _ = fmt.Fprintf(w, "   ✎ autofixed and re-staged %s\n", f)
	}
	for _, f := range fix.partial {
		_, _ = fmt.Fprintf(w, "   ⚠ not autofixed, has unstaged changes: %s\n", f)
	}
}
