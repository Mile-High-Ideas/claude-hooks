package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNodeBinNames(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		pathExt string
		want    []string
	}{
		{"posix tries the bare name only", "linux", ".EXE;.CMD", []string{"tsc"}},
		{"darwin ignores PATHEXT", "darwin", "", []string{"tsc"}},
		{"windows tries PATHEXT, lowercased, then bare", "windows", ".EXE;.CMD", []string{"tsc.exe", "tsc.cmd", "tsc"}},
		{"windows falls back to the cmd.exe default", "windows", "", []string{"tsc.com", "tsc.exe", "tsc.bat", "tsc.cmd", "tsc"}},
		{"windows skips empty PATHEXT entries", "windows", ".EXE;; .CMD ;", []string{"tsc.exe", "tsc.cmd", "tsc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodeBinNames("tsc", tt.goos, tt.pathExt)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("nodeBinNames() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveNodeBinNamedWindowsLayouts(t *testing.T) {
	windowsNames := nodeBinNames("tsc", "windows", ".EXE;.CMD")

	writeFiles := func(t *testing.T, root string, files ...string) string {
		t.Helper()
		bin := filepath.Join(root, "node_modules", ".bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(bin, f), []byte("x"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return bin
	}

	tests := []struct {
		name  string
		files []string
		want  string
	}{
		// #1290: bun on Windows writes only tsc.exe and tsc.bunx.
		{"bun layout finds the .exe", []string{"tsc.exe", "tsc.bunx"}, "tsc.exe"},
		// npm on Windows writes a POSIX script named tsc next to tsc.cmd.
		{"npm layout prefers .cmd over the shell script", []string{"tsc", "tsc.cmd", "tsc.ps1"}, "tsc.cmd"},
		{"bare name is the last resort", []string{"tsc"}, "tsc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			bin := writeFiles(t, root, tt.files...)
			app := filepath.Join(root, "apps", "web")
			if err := os.MkdirAll(app, 0o755); err != nil {
				t.Fatal(err)
			}
			got, found := resolveNodeBinNamed(app, "tsc", windowsNames)
			if want := filepath.Join(bin, tt.want); got != want || !found {
				t.Errorf("resolveNodeBinNamed() = (%q, %v), want (%q, true)", got, found, want)
			}
		})
	}

	t.Run("only a .bunx shim is not runnable", func(t *testing.T) {
		root := t.TempDir()
		writeFiles(t, root, "tsc.bunx")
		got, found := resolveNodeBinNamed(root, "tsc", windowsNames)
		if got != "tsc" || found {
			t.Errorf("resolveNodeBinNamed() = (%q, %v), want (%q, false)", got, found, "tsc")
		}
	})
}
