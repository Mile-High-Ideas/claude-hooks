package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// filesToCheck returns the files a run checks: the staged files, or with
// --all-files every tracked file that exists in the working tree.
func filesToCheck() ([]string, error) {
	if allFiles {
		return getTrackedFiles()
	}
	return getStagedFiles()
}

// getTrackedFiles returns every file in the index under the current
// directory, relative to it, skipping any deleted from the working tree. It
// backs --all-files, which feeds them to the checks in place of the staged list.
func getTrackedFiles() ([]string, error) {
	output, err := exec.Command("git", "ls-files").Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range parseStagedFiles(string(output)) {
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
		}
	}
	return files, nil
}

// getStagedFiles returns a list of staged files from git
// It only returns files that are Added, Copied, Modified, or Renamed
// Paths are relative to the current working directory
func getStagedFiles() ([]string, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-only", "--diff-filter=ACMR", "--relative")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return parseStagedFiles(string(output)), nil
}

// parseStagedFiles parses git diff output into a slice of file paths
func parseStagedFiles(output string) []string {
	var files []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// getNewlyAddedFiles returns a set of files that are newly added (not modified) in the staging area.
//
// With --all-files nothing is being added, so the set is empty and every file
// is checked as an existing one.
func getNewlyAddedFiles() (map[string]bool, error) {
	if allFiles {
		return map[string]bool{}, nil
	}
	cmd := exec.Command("git", "diff", "--cached", "--name-only", "--diff-filter=A", "--relative")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result[line] = true
		}
	}
	return result, nil
}

// narrowToAddedFiles filters stagedFiles down to those that are newly added
// (git status "A"). It backs the statusFilter:"added" escape hatch shared by
// the stub, missing-tests, and redundant-createdAt checks, letting mechanical
// bulk refactors (e.g. a package rename) move existing files without tripping
// checks that gate pre-existing violations.
//
// Callers pass absolute paths (stagedAbs) while getNewlyAddedFiles reports
// repo-relative paths, so each file is matched on either form — the earlier
// inline implementations compared only the absolute form against the relative
// set, which silently matched nothing.
func narrowToAddedFiles(stagedFiles []string, projectRoot string) ([]string, error) {
	addedSet, err := getNewlyAddedFiles()
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(stagedFiles))
	for _, f := range stagedFiles {
		if addedSet[f] {
			filtered = append(filtered, f)
			continue
		}
		if rel, err := filepath.Rel(projectRoot, f); err == nil {
			if addedSet[filepath.ToSlash(rel)] {
				filtered = append(filtered, f)
			}
		}
	}
	return filtered, nil
}

// categorizeFiles separates files into app-specific groups and detects shared path changes
// Returns:
//   - appFiles: map of app name to files belonging to that app
//   - sharedChanged: true if any file matches a shared path
func categorizeFiles(files []string, apps map[string]AppConfig, sharedPaths []string) (appFiles map[string][]string, sharedChanged bool) {
	appFiles = make(map[string][]string)

	for _, file := range files {
		categorized := false

		// Check if file belongs to any app
		for appName, appConfig := range apps {
			if strings.HasPrefix(file, appConfig.Path+"/") {
				appFiles[appName] = append(appFiles[appName], file)
				categorized = true
				break
			}
		}

		// If not in any app, check if it's in a shared path
		if !categorized {
			for _, sharedPath := range sharedPaths {
				if strings.HasPrefix(file, sharedPath) {
					sharedChanged = true
					break
				}
			}
		}
	}

	return appFiles, sharedChanged
}
