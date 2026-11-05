package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilterGoFiles(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		paths []string
		want  []string
	}{
		{
			name:  "filters .go files in configured path",
			files: []string{"apps/vendor-sync/main.go", "apps/vendor-sync/parser.go", "apps/web/page.tsx"},
			paths: []string{"apps/vendor-sync"},
			want:  []string{"apps/vendor-sync/main.go", "apps/vendor-sync/parser.go"},
		},
		{
			name:  "excludes .go files outside configured paths",
			files: []string{"apps/vendor-sync/main.go", "apps/other/main.go"},
			paths: []string{"apps/vendor-sync"},
			want:  []string{"apps/vendor-sync/main.go"},
		},
		{
			name:  "excludes non-.go files",
			files: []string{"apps/vendor-sync/main.go", "apps/vendor-sync/README.md"},
			paths: []string{"apps/vendor-sync"},
			want:  []string{"apps/vendor-sync/main.go"},
		},
		{
			name:  "handles multiple configured paths",
			files: []string{"apps/vendor-sync/main.go", "apps/api/server.go", "apps/web/page.tsx"},
			paths: []string{"apps/vendor-sync", "apps/api"},
			want:  []string{"apps/vendor-sync/main.go", "apps/api/server.go"},
		},
		{
			name:  "handles nested files in configured path",
			files: []string{"apps/vendor-sync/internal/parser/csv.go"},
			paths: []string{"apps/vendor-sync"},
			want:  []string{"apps/vendor-sync/internal/parser/csv.go"},
		},
		{
			name:  "returns all .go files when no paths configured",
			files: []string{"main.go", "pkg/lib.go", "README.md"},
			paths: []string{},
			want:  []string{"main.go", "pkg/lib.go"},
		},
		{
			name:  "returns empty for no matching files",
			files: []string{"apps/web/page.tsx", "README.md"},
			paths: []string{"apps/vendor-sync"},
			want:  nil,
		},
		{
			name:  "handles empty file list",
			files: []string{},
			paths: []string{"apps/vendor-sync"},
			want:  nil,
		},
		{
			name:  "handles path with trailing slash",
			files: []string{"apps/vendor-sync/main.go"},
			paths: []string{"apps/vendor-sync/"},
			want:  []string{"apps/vendor-sync/main.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterGoFiles(tt.files, tt.paths)
			if len(got) != len(tt.want) {
				t.Errorf("filterGoFiles() = %v (len=%d), want %v (len=%d)", got, len(got), tt.want, len(tt.want))
				return
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("filterGoFiles()[%d] = %v, want %v", i, got[i], want)
				}
			}
		})
	}
}

func TestCollectGoDirectories(t *testing.T) {
	tests := []struct {
		name    string
		goFiles []string
		paths   []string
		want    []string
	}{
		{
			name:    "collects unique directories",
			goFiles: []string{"apps/vendor-sync/main.go", "apps/vendor-sync/parser.go"},
			paths:   []string{"apps/vendor-sync"},
			want:    []string{"apps/vendor-sync"},
		},
		{
			name:    "handles multiple configured paths",
			goFiles: []string{"apps/vendor-sync/main.go", "apps/api/server.go"},
			paths:   []string{"apps/vendor-sync", "apps/api"},
			want:    []string{"apps/vendor-sync", "apps/api"},
		},
		{
			name:    "returns empty for no Go files",
			goFiles: []string{},
			paths:   []string{"apps/vendor-sync"},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectGoDirectories(tt.goFiles, tt.paths)
			if len(got) != len(tt.want) {
				t.Errorf("collectGoDirectories() = %v (len=%d), want %v (len=%d)", got, len(got), tt.want, len(tt.want))
				return
			}
			// Check all expected dirs are present (order may vary due to map iteration)
			for _, want := range tt.want {
				found := false
				for _, g := range got {
					if g == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("collectGoDirectories() missing expected dir %q", want)
				}
			}
		})
	}
}

func TestHasCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{
			name:    "ls exists on all systems",
			command: "ls",
			want:    true,
		},
		{
			name:    "go command typically exists",
			command: "go",
			want:    true,
		},
		{
			name:    "nonexistent command returns false",
			command: "this-command-definitely-does-not-exist-12345",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip go check if go is not installed
			if tt.command == "go" {
				if _, err := exec.LookPath("go"); err != nil {
					t.Skip("go not installed")
				}
			}
			if got := hasCommand(tt.command); got != tt.want {
				t.Errorf("hasCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestCheckGoLint_EmptyFiles(t *testing.T) {
	config := GoLintConfig{
		Paths: []string{"apps/vendor-sync"},
		Tool:  "golangci-lint",
	}

	// No Go files staged - should return nil (skip)
	err := checkGoLint([]string{}, config)
	if err != nil {
		t.Errorf("checkGoLint() with empty files = %v, want nil", err)
	}

	// Only non-Go files - should return nil (skip)
	err = checkGoLint([]string{"apps/vendor-sync/README.md", "apps/web/page.tsx"}, config)
	if err != nil {
		t.Errorf("checkGoLint() with no .go files = %v, want nil", err)
	}

	// Go files but not in configured path - should return nil (skip)
	err = checkGoLint([]string{"apps/other/main.go"}, config)
	if err != nil {
		t.Errorf("checkGoLint() with .go files outside path = %v, want nil", err)
	}
}

func TestCheckGoLint_ToolFallback(t *testing.T) {
	// Create temp directory with valid Go code
	tmpDir, err := os.MkdirTemp("", "go-lint-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	goDir := filepath.Join(tmpDir, "apps", "vendor-sync")
	if err := os.MkdirAll(goDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a valid Go file
	goFile := filepath.Join(goDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create go.mod
	goMod := filepath.Join(goDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Save and restore working directory
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldDir) }()

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Test with go-vet explicitly
	config := GoLintConfig{
		Paths: []string{"apps/vendor-sync"},
		Tool:  "go-vet",
	}

	stagedFiles := []string{"apps/vendor-sync/main.go"}

	// This should use go vet since we specified it
	err = checkGoLint(stagedFiles, config)
	if err != nil {
		t.Errorf("checkGoLint() with go-vet on valid code = %v, want nil", err)
	}
}

func TestCheckGoLint_LintingFailure(t *testing.T) {
	// Skip if go is not installed
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	// Create temp directory with Go code that has issues
	tmpDir, err := os.MkdirTemp("", "go-lint-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	goDir := filepath.Join(tmpDir, "apps", "vendor-sync")
	if err := os.MkdirAll(goDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a Go file with an issue (unused variable for go vet)
	// Using Printf without proper format - go vet will catch this
	goFile := filepath.Join(goDir, "main.go")
	badCode := `package main

import "fmt"

func main() {
	fmt.Printf("%d", "not an int")
}
`
	if err := os.WriteFile(goFile, []byte(badCode), 0644); err != nil {
		t.Fatal(err)
	}

	// Create go.mod
	goMod := filepath.Join(goDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Save and restore working directory
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldDir) }()

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	config := GoLintConfig{
		Paths: []string{"apps/vendor-sync"},
		Tool:  "go-vet",
	}

	stagedFiles := []string{"apps/vendor-sync/main.go"}

	// This should fail because of the Printf format issue
	err = checkGoLint(stagedFiles, config)
	if err == nil {
		t.Error("checkGoLint() with bad code = nil, want error")
	}
}

func TestCheckGoLint_LintingSuccess(t *testing.T) {
	// Skip if go is not installed
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}

	// Create temp directory with valid Go code
	tmpDir, err := os.MkdirTemp("", "go-lint-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	goDir := filepath.Join(tmpDir, "apps", "vendor-sync")
	if err := os.MkdirAll(goDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a valid Go file
	goFile := filepath.Join(goDir, "main.go")
	goodCode := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}
`
	if err := os.WriteFile(goFile, []byte(goodCode), 0644); err != nil {
		t.Fatal(err)
	}

	// Create go.mod
	goMod := filepath.Join(goDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Save and restore working directory
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldDir) }()

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	config := GoLintConfig{
		Paths: []string{"apps/vendor-sync"},
		Tool:  "go-vet",
	}

	stagedFiles := []string{"apps/vendor-sync/main.go"}

	// This should succeed
	err = checkGoLint(stagedFiles, config)
	if err != nil {
		t.Errorf("checkGoLint() with valid code = %v, want nil", err)
	}
}

func TestGoLintConfig_DefaultTool(t *testing.T) {
	// Test that empty tool defaults to golangci-lint behavior
	config := GoLintConfig{
		Paths: []string{"apps/vendor-sync"},
		Tool:  "", // Empty should default to golangci-lint
	}

	// We test this indirectly through checkGoLint with no matching files
	// The function should not error
	err := checkGoLint([]string{}, config)
	if err != nil {
		t.Errorf("checkGoLint() with default tool config = %v, want nil", err)
	}
}

func TestGoLintCommand(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		wantName string
		wantArgs []string
	}{
		{
			name:     "golangci-lint waits for another instance's lock",
			tool:     "golangci-lint",
			wantName: "golangci-lint",
			wantArgs: []string{"run", "--allow-serial-runners", "./..."},
		},
		{
			name:     "go vet fallback",
			tool:     "go-vet",
			wantName: "go",
			wantArgs: []string{"vet", "./..."},
		},
		{
			name:     "unknown tool runs go vet",
			tool:     "staticcheck",
			wantName: "go",
			wantArgs: []string{"vet", "./..."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotArgs := goLintCommand(tt.tool)
			if gotName != tt.wantName {
				t.Errorf("goLintCommand(%q) name = %q, want %q", tt.tool, gotName, tt.wantName)
			}
			if strings.Join(gotArgs, " ") != strings.Join(tt.wantArgs, " ") {
				t.Errorf("goLintCommand(%q) args = %q, want %q", tt.tool, gotArgs, tt.wantArgs)
			}
		})
	}
}
