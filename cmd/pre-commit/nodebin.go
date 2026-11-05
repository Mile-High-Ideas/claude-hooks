package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// defaultWindowsPathExt is what cmd.exe uses when PATHEXT is unset.
const defaultWindowsPathExt = ".COM;.EXE;.BAT;.CMD"

// resolveNodeBin walks up from startDir looking for node_modules/.bin/tool and
// returns its absolute path with found=true. This lets the gate run each
// project's *installed* CLI (tsc, tsgo, tsc-files, eslint, oxlint, lint-staged,
// convex, prettier) rather than a global copy or an npx/bunx-fetched one, so the
// gate can never drift from the versions `bun run`/CI use.
//
// On Windows the package managers write no runnable extensionless file: bun
// writes tsc.exe (plus tsc.bunx), npm writes tsc.cmd beside a POSIX shell
// script named tsc. So there the PATHEXT names are tried first, and the bare
// name last.
//
// A missing or half-written node_modules (fresh clone, an install still in
// flight, or a broken .bin symlink) makes os.Stat fail, so found is false and
// the caller decides how to degrade — never treating a transient state as a
// real result. When not found the returned name is the bare tool, which callers
// must NOT run through a network-fetching runner.
func resolveNodeBin(startDir, tool string) (string, bool) {
	return resolveNodeBinNamed(startDir, tool, nodeBinNames(tool, runtime.GOOS, os.Getenv("PATHEXT")))
}

// nodeBinNames lists the file names that can hold tool in node_modules/.bin on
// goos, in the order to try them. pathExt is the Windows PATHEXT value.
func nodeBinNames(tool, goos, pathExt string) []string {
	if goos != "windows" {
		return []string{tool}
	}
	if strings.TrimSpace(pathExt) == "" {
		pathExt = defaultWindowsPathExt
	}
	var names []string
	for _, ext := range strings.Split(pathExt, ";") {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		names = append(names, tool+ext)
	}
	return append(names, tool)
}

// resolveNodeBinNamed is resolveNodeBin with the candidate file names given.
func resolveNodeBinNamed(startDir, tool string, names []string) (string, bool) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return tool, false
	}
	for {
		if found, ok := firstFile(filepath.Join(dir, "node_modules", ".bin"), names); ok {
			return found, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return tool, false
		}
		dir = parent
	}
}

// firstFile returns the first of names that is a regular file in dir.
func firstFile(dir string, names []string) (string, bool) {
	for _, name := range names {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}
