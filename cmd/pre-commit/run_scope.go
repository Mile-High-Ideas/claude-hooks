package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// applyRunScope narrows a loaded config to what --app and --skip ask for.
// CI uses them to split one suite across parallel jobs: `--check tests --app
// web` runs only web's tests, and `--skip lint,typecheck` runs the rest of
// the pipeline without the heavy checks.
func applyRunScope(config *Config, apps, skip string) error {
	if err := restrictApps(config, splitList(apps)); err != nil {
		return err
	}
	return skipFeatures(&config.Features, splitList(skip))
}

// restrictApps keeps only the named apps. An unknown name is an error, so a
// typo cannot quietly check nothing.
func restrictApps(config *Config, names []string) error {
	if len(names) == 0 {
		return nil
	}
	kept := make(map[string]AppConfig, len(names))
	for _, name := range names {
		app, ok := config.Apps[name]
		if !ok {
			return fmt.Errorf("--app %s: no such app in .pre-commit.json (have: %s)", name, strings.Join(sortedKeys(config.Apps), ", "))
		}
		kept[name] = app
	}
	config.Apps = kept
	return nil
}

// skipFeatures turns off each named feature, by its .pre-commit.json name.
// An unknown name is an error, for the same reason as restrictApps.
func skipFeatures(features *Features, names []string) error {
	if len(names) == 0 {
		return nil
	}
	fields := featureFieldsByName(features)
	for _, name := range names {
		field, ok := fields[name]
		if !ok {
			return fmt.Errorf("--skip %s: no such feature in .pre-commit.json", name)
		}
		field.SetBool(false)
	}
	return nil
}

// featureFieldsByName maps each boolean feature's JSON name to its field.
func featureFieldsByName(features *Features) map[string]reflect.Value {
	value := reflect.ValueOf(features).Elem()
	fields := make(map[string]reflect.Value, value.NumField())
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() != reflect.Bool {
			continue
		}
		name, _, _ := strings.Cut(value.Type().Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields[name] = field
		}
	}
	return fields
}

// splitList splits a comma-separated flag value, dropping blanks.
func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func sortedKeys(apps map[string]AppConfig) []string {
	keys := make([]string, 0, len(apps))
	for key := range apps {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
