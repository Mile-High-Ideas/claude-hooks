package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// setupEnv runs the project's setup script in the worktree and returns the
// KEY=VALUE lines it printed, which are added to the commit's environment.
// Anything it writes to stderr goes to the run log. A project uses this for
// what only it needs, such as pinning a Node version or moving TMPDIR.
func setupEnv(worktree, script string, log io.Writer) ([]string, error) {
	if script == "" {
		return nil, nil
	}
	path := script
	if !filepath.IsAbs(path) {
		path = filepath.Join(worktree, script)
	}
	cmd := exec.Command(path)
	cmd.Dir = worktree
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("setup script %s failed: %w", script, err)
	}
	return parseEnvLines(stdout.String())
}

// parseEnvLines reads KEY=VALUE lines, ignoring blank lines and # comments.
func parseEnvLines(out string) ([]string, error) {
	var env []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("setup script printed %q; want KEY=VALUE lines", line)
		}
		env = append(env, line)
	}
	return env, nil
}
