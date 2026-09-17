package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestResolveConvexMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "unset defaults to codegen", mode: "", want: ConvexModeCodegen},
		{name: "explicit codegen", mode: ConvexModeCodegen, want: ConvexModeCodegen},
		{name: "explicit dev", mode: ConvexModeDev, want: ConvexModeDev},
		// A typo must not deploy. Only an exact "dev" opts in.
		{name: "wrong case falls back to codegen", mode: "DEV", want: ConvexModeCodegen},
		{name: "unknown value falls back to codegen", mode: "deploy", want: ConvexModeCodegen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveConvexMode(tt.mode); got != tt.want {
				t.Errorf("resolveConvexMode(%q) = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

func TestConvexCommandArgs(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want []string
	}{
		{
			name: "codegen validates without touching the deployment",
			mode: ConvexModeCodegen,
			// --typecheck enable rather than the CLI default of "try", which
			// silently skips when tsc cannot run.
			want: []string{"codegen", "--typecheck", "enable"},
		},
		{
			name: "dev pushes to the configured deployment",
			mode: ConvexModeDev,
			want: []string{"dev", "--once"},
		},
		{
			name: "unset uses the codegen arguments",
			mode: "",
			want: []string{"codegen", "--typecheck", "enable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := convexCommandArgs(tt.mode); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("convexCommandArgs(%q) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

func TestConvexCommandArgsDefaultDoesNotDeploy(t *testing.T) {
	// The regression this whole change exists to prevent: the default path
	// must never invoke `convex dev`, which is documented as pushing code to
	// the configured deployment.
	for _, mode := range []string{"", ConvexModeCodegen, "typo"} {
		args := convexCommandArgs(mode)
		if args[0] != "codegen" {
			t.Errorf("mode %q ran %q; the default must be codegen", mode, args[0])
		}
		for _, arg := range args {
			if arg == "dev" || arg == "--once" {
				t.Errorf("mode %q produced deploying arguments: %v", mode, args)
			}
		}
	}
}

func TestEvaluateConvexRun(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		marker      string
		output      string
		cmdErr      error
		expectErr   bool
		errContains string
	}{
		{
			// codegen prints no completion line. Requiring a marker would fail
			// every successful run.
			name:      "codegen passes on a clean exit with no marker",
			mode:      ConvexModeCodegen,
			output:    "Generating server code...\nRunning TypeScript...",
			expectErr: false,
		},
		{
			name:        "codegen fails on a non-zero exit",
			mode:        ConvexModeCodegen,
			output:      "error TS2339: Property does not exist",
			cmdErr:      errors.New("exit status 1"),
			expectErr:   true,
			errContains: "codegen",
		},
		{
			name:      "dev passes when the marker is present",
			mode:      ConvexModeDev,
			output:    "Starting Convex dev...\nConvex functions ready!\nWatching...",
			expectErr: false,
		},
		{
			name:        "dev fails when the marker is missing",
			mode:        ConvexModeDev,
			output:      "Error: Failed to compile functions",
			expectErr:   true,
			errContains: "success marker",
		},
		{
			name:      "dev honours a custom marker",
			mode:      ConvexModeDev,
			marker:    "Build complete!",
			output:    "Compiling...\nBuild complete!\nDone.",
			expectErr: false,
		},
		{
			name:        "dev reports the custom marker it wanted",
			mode:        ConvexModeDev,
			marker:      "Custom success!",
			output:      "Convex functions ready!",
			expectErr:   true,
			errContains: "Custom success!",
		},
		{
			name:        "dev fails on a non-zero exit even with the marker",
			mode:        ConvexModeDev,
			output:      "Convex functions ready!",
			cmdErr:      errors.New("exit status 1"),
			expectErr:   true,
			errContains: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := evaluateConvexRun(tt.mode, tt.marker, tt.output, tt.cmdErr)

			if !tt.expectErr {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
				return
			}

			if err == nil {
				t.Error("expected error but got nil")
				return
			}
			if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
			}
		})
	}
}

func TestEvaluateConvexRunWrapsCommandError(t *testing.T) {
	// Callers should be able to inspect the underlying failure.
	cmdErr := errors.New("exit status 1")

	err := evaluateConvexRun(ConvexModeCodegen, "", "output", cmdErr)

	if !errors.Is(err, cmdErr) {
		t.Errorf("expected the command error to be wrapped, got %v", err)
	}
}

func TestCheckConvexRequiresPath(t *testing.T) {
	if err := checkConvex(ConvexConfig{Path: ""}); err == nil {
		t.Error("expected an error for an empty path")
	} else if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected a path error, got %q", err.Error())
	}
}

func TestDefaultSuccessMarker(t *testing.T) {
	if DefaultConvexSuccessMarker != "Convex functions ready!" {
		t.Errorf("expected default marker to be %q, got %q",
			"Convex functions ready!", DefaultConvexSuccessMarker)
	}
}

func TestConvexConfig(t *testing.T) {
	tests := []struct {
		name           string
		config         ConvexConfig
		expectedPath   string
		expectedMarker string
		expectedMode   string
	}{
		{
			name: "defaults when nothing is specified",
			config: ConvexConfig{
				Path: "packages/backend",
			},
			expectedPath:   "packages/backend",
			expectedMarker: DefaultConvexSuccessMarker,
			expectedMode:   ConvexModeCodegen,
		},
		{
			name: "custom marker and dev mode when specified",
			config: ConvexConfig{
				Path:          "convex",
				Mode:          ConvexModeDev,
				SuccessMarker: "Ready!",
			},
			expectedPath:   "convex",
			expectedMarker: "Ready!",
			expectedMode:   ConvexModeDev,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config.Path != tt.expectedPath {
				t.Errorf("expected path %q, got %q", tt.expectedPath, tt.config.Path)
			}

			marker := tt.config.SuccessMarker
			if marker == "" {
				marker = DefaultConvexSuccessMarker
			}
			if marker != tt.expectedMarker {
				t.Errorf("expected marker %q, got %q", tt.expectedMarker, marker)
			}

			if got := resolveConvexMode(tt.config.Mode); got != tt.expectedMode {
				t.Errorf("expected mode %q, got %q", tt.expectedMode, got)
			}
		})
	}
}
