package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// convexDeploymentVariables are the variables the Convex CLI uses to pick a
// deployment. CONVEX_DEPLOY_KEY wins over CONVEX_DEPLOYMENT when both are set,
// but either one naming production is enough to refuse: the guard does not try
// to replicate the CLI's precedence rules, it only needs to know that a
// production credential is within reach.
var convexDeploymentVariables = []string{"CONVEX_DEPLOY_KEY", "CONVEX_DEPLOYMENT"}

// convexEnvFiles are the dotenv files the Convex CLI loads from its working
// directory, in the order it reads them. The process environment takes
// precedence over both.
var convexEnvFiles = []string{".env.local", ".env"}

// convexProductionPrefix marks a production deployment in both variables:
// deploy keys look like "prod:<deployment>|<secret>" and CONVEX_DEPLOYMENT
// looks like "prod:<deployment>". Dev, preview, local and anonymous
// deployments use other prefixes.
const convexProductionPrefix = "prod:"

// convexProductionSource names where a production setting was found. It
// deliberately carries no value: the guard must never print a deploy key or
// deployment name, only the variable and the place it came from.
type convexProductionSource struct {
	Variable string
	Origin   string
}

// convexProductionGuardEnabled reports whether the production guard applies.
// It is on unless .pre-commit.json explicitly sets
// convex.refuseProductionDeployment to false.
func convexProductionGuardEnabled(config ConvexConfig) bool {
	return config.RefuseProductionDeployment == nil || *config.RefuseProductionDeployment
}

// isConvexProductionValue reports whether a deployment variable's value
// selects a production deployment. Surrounding whitespace is ignored.
func isConvexProductionValue(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), convexProductionPrefix)
}

// findConvexProductionDeployment looks for a production deployment setting in
// the process environment (via lookupEnv) and in the dotenv files the Convex
// CLI would load from dir. It returns nil when nothing names production.
func findConvexProductionDeployment(dir string, lookupEnv func(string) (string, bool)) (*convexProductionSource, error) {
	for _, variable := range convexDeploymentVariables {
		if value, ok := lookupEnv(variable); ok && isConvexProductionValue(value) {
			return &convexProductionSource{Variable: variable, Origin: "the process environment"}, nil
		}
	}

	for _, name := range convexEnvFiles {
		path := filepath.Join(dir, name)
		values, err := readDotenvVariables(path, convexDeploymentVariables)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		for _, variable := range convexDeploymentVariables {
			if isConvexProductionValue(values[variable]) {
				return &convexProductionSource{Variable: variable, Origin: path}, nil
			}
		}
	}

	return nil, nil
}

// readDotenvVariables returns the values of the wanted variables from a dotenv
// file. A missing file yields an empty map. Only KEY=VALUE lines are
// understood, with optional "export " and surrounding quotes; that is enough to
// read a prefix, which is all the guard needs.
func readDotenvVariables(path string, wanted []string) (map[string]string, error) {
	values := map[string]string{}

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	want := map[string]bool{}
	for _, name := range wanted {
		want[name] = true
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		key, value, ok := parseDotenvLine(scanner.Text())
		if ok && want[key] {
			values[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}

// parseDotenvLine splits one dotenv line into key and unquoted value. ok is
// false for blank lines, comments and lines without "=".
func parseDotenvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")

	key, value, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}

	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' || first == '\'') && first == last {
			value = value[1 : len(value)-1]
		}
	}

	return strings.TrimSpace(key), value, true
}

// convexProductionError is the refusal shown when validation would run against
// production. It names the variable and where it was set, never its value.
func convexProductionError(source convexProductionSource) error {
	return fmt.Errorf(
		"convex validation refused: %s in %s selects a production deployment (%q prefix).\n"+
			"Validating against production means every checkout that commits holds a production deploy key.\n"+
			"Run `just convex-local-setup` to point this checkout at a local deployment, then commit again.\n"+
			"To allow this anyway, set \"convex\": {\"refuseProductionDeployment\": false} in .pre-commit.json",
		source.Variable, source.Origin, convexProductionPrefix,
	)
}

// guardConvexProductionDeployment refuses to validate when the Convex CLI
// running in dir would be configured for a production deployment.
func guardConvexProductionDeployment(config ConvexConfig, lookupEnv func(string) (string, bool)) error {
	if !convexProductionGuardEnabled(config) {
		return nil
	}

	source, err := findConvexProductionDeployment(config.Path, lookupEnv)
	if err != nil {
		return fmt.Errorf("convex production guard: %w", err)
	}
	if source != nil {
		return convexProductionError(*source)
	}

	return nil
}
