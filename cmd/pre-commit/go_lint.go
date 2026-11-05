package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// checkGoLint runs Go linting on staged Go files
func checkGoLint(stagedFiles []string, config GoLintConfig) error {
	// Filter to only .go files matching configured paths
	goFiles := filterGoFiles(stagedFiles, config.Paths)
	if len(goFiles) == 0 {
		return nil
	}

	// Determine which directories have Go files
	dirs := collectGoDirectories(goFiles, config.Paths)
	if len(dirs) == 0 {
		return nil
	}

	// Determine which tool to use
	tool := config.Tool
	if tool == "" {
		tool = "golangci-lint"
	}

	// Check if preferred tool is available, fall back to go vet
	if tool == "golangci-lint" && !hasCommand("golangci-lint") {
		fmt.Println("   golangci-lint not found, falling back to go vet")
		tool = "go-vet"
	}

	// Run linter in each directory. Always capture so the output can be
	// persisted to a report; in verbose mode it's echoed afterward.
	var lintErrors []string
	var combined strings.Builder
	for _, dir := range dirs {
		if !compactMode() {
			fmt.Printf("   Linting Go code in %s...\n", dir)
		}

		name, args := goLintCommand(tool)
		out, err := runCommandCapturedInDir(dir, name, args...)
		if !compactMode() && out != "" {
			fmt.Print(out)
		}
		fmt.Fprintf(&combined, "===== %s =====\n%s\n", dir, out)

		if err != nil {
			lintErrors = append(lintErrors, fmt.Sprintf("%s: %v", dir, err))
		}
	}

	failed := len(lintErrors) > 0
	_ = writeRunReport("go-lint", "Go linting", combined.String(), failed)
	if failed {
		printReportHint("go-lint/")
		return fmt.Errorf("go lint failed:\n  %s", strings.Join(lintErrors, "\n  "))
	}

	return nil
}

// goLintCommand returns the command checkGoLint runs in each directory for
// the given tool: "golangci-lint", or anything else for go vet.
//
// golangci-lint takes a file lock in $TMPDIR at start and, by default, exits
// with "parallel golangci-lint is running" when another instance holds it.
// commit-queue runs two suites at once with one shared TMPDIR per repo, so the
// second suite to reach Go lint failed with no finding at all.
// --allow-serial-runners makes it wait for the lock instead.
func goLintCommand(tool string) (string, []string) {
	if tool == "golangci-lint" {
		return "golangci-lint", []string{"run", "--allow-serial-runners", "./..."}
	}
	return "go", []string{"vet", "./..."}
}

// filterGoFiles filters staged files to only .go files matching configured paths
func filterGoFiles(files []string, paths []string) []string {
	var result []string
	for _, file := range files {
		if !strings.HasSuffix(file, ".go") {
			continue
		}

		// If no paths configured, include all .go files
		if len(paths) == 0 {
			result = append(result, file)
			continue
		}

		// Check if file matches any configured path
		for _, path := range paths {
			// Normalize path to ensure consistent matching
			normalizedPath := strings.TrimSuffix(path, "/")
			if strings.HasPrefix(file, normalizedPath+"/") || file == normalizedPath {
				result = append(result, file)
				break
			}
		}
	}
	return result
}

// collectGoDirectories extracts unique directories from Go files that match configured paths
func collectGoDirectories(goFiles []string, paths []string) []string {
	dirSet := make(map[string]bool)

	for _, file := range goFiles {
		// Find which configured path this file belongs to
		for _, path := range paths {
			normalizedPath := strings.TrimSuffix(path, "/")
			if strings.HasPrefix(file, normalizedPath+"/") {
				dirSet[normalizedPath] = true
				break
			}
		}
	}

	// If no paths configured but we have Go files, use the file directories
	if len(paths) == 0 && len(goFiles) > 0 {
		for _, file := range goFiles {
			dir := filepath.Dir(file)
			dirSet[dir] = true
		}
	}

	var dirs []string
	for dir := range dirSet {
		dirs = append(dirs, dir)
	}
	return dirs
}

// hasCommand checks if a command is available in PATH
func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
