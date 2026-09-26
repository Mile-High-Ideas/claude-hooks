bindir := "bin"

# List available recipes
default:
    @just --list

# Activate the in-repo git pre-commit hook. One-time setup per clone.
# Points core.hooksPath at .githooks so the wrapper script
# (rebuild binary → run binary → go test ./...) fires on every commit.
setup-hooks:
    git config core.hooksPath .githooks
    @echo "✓ Pre-commit hook installed."
    @echo "  Each commit will rebuild bin/pre-commit, run it against"
    @echo "  .pre-commit.json (goLint + changelog), and run go test ./..."

# Build all binaries
build: check-workspace auto-convex-gen auto-lingui-extract auto-tiers-gen block-destructive-commands block-generated-files block-infrastructure block-lint-workarounds block-pre-commit-exceptions block-redundant-createdat changelog-add convex-gen docs-tracker enforce-tests-on-commit format-on-save markdown-formatter pre-commit smart-lint smart-test track-edited-files validate-convex validate-frontend-structure validate-srp validate-test-files validate-next

# Fail if any executable exists at the repo root with the same name as a cmd/*/ subdir.
# These get created when someone runs `go build ./cmd/<name>` from the repo root without -o,
# which pollutes the workspace. Legit builds go to bin/ via `just <name>`.
check-workspace:
    #!/usr/bin/env bash
    set -euo pipefail
    shopt -s nullglob
    stray=()
    for cmd in cmd/*/; do
        name=$(basename "$cmd")
        if [ -f "$name" ]; then
            stray+=("$name")
        fi
    done
    if [ ${#stray[@]} -gt 0 ]; then
        echo "Error: found stray binaries at repo root:" >&2
        for name in "${stray[@]}"; do
            echo "  - $name" >&2
        done
        echo "" >&2
        echo "These were probably built with 'go build ./cmd/<name>' from the repo root." >&2
        echo "Use 'just <name>' or 'just build' — they write to {{bindir}}/ where they belong." >&2
        echo "" >&2
        echo "Fix: just clean" >&2
        exit 1
    fi

# Individual binaries
auto-convex-gen:
    go build -o {{bindir}}/auto-convex-gen ./cmd/auto-convex-gen

auto-lingui-extract:
    go build -o {{bindir}}/auto-lingui-extract ./cmd/auto-lingui-extract

auto-tiers-gen:
    go build -o {{bindir}}/auto-tiers-gen ./cmd/auto-tiers-gen

block-destructive-commands:
    go build -o {{bindir}}/block-destructive-commands ./cmd/block-destructive-commands

block-generated-files:
    go build -o {{bindir}}/block-generated-files ./cmd/block-generated-files

block-infrastructure:
    go build -o {{bindir}}/block-infrastructure ./cmd/block-infrastructure

block-lint-workarounds:
    go build -o {{bindir}}/block-lint-workarounds ./cmd/block-lint-workarounds

block-pre-commit-exceptions:
    go build -o {{bindir}}/block-pre-commit-exceptions ./cmd/block-pre-commit-exceptions

block-redundant-createdat:
    go build -o {{bindir}}/block-redundant-createdat ./cmd/block-redundant-createdat

changelog-add:
    go build -o {{bindir}}/changelog-add ./cmd/changelog-add

convex-gen:
    go build -o {{bindir}}/convex-gen ./cmd/convex-gen

docs-tracker:
    go build -o {{bindir}}/docs-tracker ./cmd/docs-tracker

enforce-tests-on-commit:
    go build -o {{bindir}}/enforce-tests-on-commit ./cmd/enforce-tests-on-commit

format-on-save:
    go build -o {{bindir}}/format-on-save ./cmd/format-on-save

markdown-formatter:
    go build -o {{bindir}}/markdown-formatter ./cmd/markdown-formatter

pre-commit:
    go build -o {{bindir}}/pre-commit ./cmd/pre-commit

smart-lint:
    go build -o {{bindir}}/smart-lint ./cmd/smart-lint

smart-test:
    go build -o {{bindir}}/smart-test ./cmd/smart-test

track-edited-files:
    go build -o {{bindir}}/track-edited-files ./cmd/track-edited-files

validate-convex:
    go build -o {{bindir}}/validate-convex ./cmd/validate-convex

validate-frontend-structure:
    go build -o {{bindir}}/validate-frontend-structure ./cmd/validate-frontend-structure

validate-srp:
    go build -o {{bindir}}/validate-srp ./cmd/validate-srp

validate-test-files:
    go build -o {{bindir}}/validate-test-files ./cmd/validate-test-files

validate-next:
    go build -o {{bindir}}/validate-next ./cmd/validate-next

# Run all tests
test:
    go test ./...

# Clean all binaries (bin/ and any stray root binaries from misguided `go build`s)
clean: clean-strays
    #!/usr/bin/env bash
    set -euo pipefail
    shopt -s nullglob
    rm -rf {{bindir}}/*

# Remove only stray root binaries from misguided `go build ./cmd/<name>` runs, leaving bin/ intact
clean-strays:
    #!/usr/bin/env bash
    set -euo pipefail
    for cmd in cmd/*/; do
        name=$(basename "$cmd")
        if [ -f "$name" ]; then
            echo "Removing stray root binary: $name"
            rm "$name"
        fi
    done

# Symlink built binaries into ~/.local/bin (self-updating, no sudo, never stale).
#
# Symlinks track future `just build`s, so a rebuilt binary is picked up with no
# re-install — the same self-updating property the convex-gen symlink already
# relies on. Copying into /usr/local/bin (the old behavior) froze binaries at
# install time: a stale root-owned copy there silently shadowed fresh builds on
# PATH and regenerated data-layer hooks with the pre-`-typed-returns` output.
#
# Ensure ~/.local/bin precedes /usr/local/bin on PATH.
install: build
    #!/usr/bin/env bash
    set -euo pipefail
    dest="$HOME/.local/bin"
    mkdir -p "$dest"
    echo "Symlinking binaries into $dest..."
    for bin in {{bindir}}/*; do
        [ -f "$bin" ] || continue
        ln -sf "$PWD/$bin" "$dest/$(basename "$bin")"
    done
    echo "✓ Installed. Ensure $dest precedes /usr/local/bin on your PATH."

# Cross-compile the zig-built release platforms (linux + windows).
#
# pre-commit, validate-srp, and validate-test-files depend on tree-sitter
# (CGO) for AST-based stub detection, so cross-compiling needs a C toolchain
# per target. A single tool — zig — covers linux and windows, replacing
# the old musl-cross / mingw-w64 / aarch64 toolchains:
#
#   brew install zig          (macOS)
#   snap install zig --classic / mlugg/setup-zig action  (Linux / CI)
#
# These targets are static and self-contained: linux-amd64 + linux-arm64
# are statically linked musl binaries; windows-amd64 depends only on the OS
# (KERNEL32 + Universal CRT). Override the per-target compiler with the
# CC_LINUX_AMD64 / CC_LINUX_ARM64 / CC_WINDOWS_AMD64 env vars if needed.
#
# darwin is released too, but not from here: Go's darwin runtime links
# CoreFoundation + libresolv, which need the Apple macOS SDK (license-
# restricted, non-redistributable), so zig on a Linux host cannot build it.
# CI builds darwin on a macOS runner with `just package-darwin`.
#
# Release binaries are stripped: symbol tables and DWARF are dead weight in an
# artefact that is downloaded and run, never attached to a debugger.
#
# -extldflags=-s is needed as well as -s -w. Go's own flags drop Go's DWARF,
# but the CGO objects zig cc links (tree-sitter) keep theirs until the external
# linker is told to strip too — `file` reports "not stripped" without it.
#
# Together they roughly halve each release archive, which matters because
# oversized parallel uploads were failing the release job outright — see
# Mile-High-Ideas/claude-hooks#5.
build-release: check-workspace build-linux build-linux-arm64 build-windows

# Build every release platform (macOS host only: darwin needs the Apple SDK)
build-all: check-workspace build-darwin build-release

# Build both darwin release targets (macOS host only)
build-darwin: (build-darwin-arch "arm64") (build-darwin-arch "amd64")

# darwin targets build natively with the host's clang and Apple SDK.
#
# An Apple Silicon host builds amd64 too: clang's -arch x86_64 compiles the
# tree-sitter CGO objects for Intel against the same SDK, no second machine
# needed. MACOSX_DEPLOYMENT_TARGET pins the minimum macOS to Go's own floor;
# left unset, clang stamps the SDK's version and the binaries refuse to run
# on older releases. Strip with -s -w only: Apple's linker treats -s as
# obsolete and warns.
#
# Build one darwin target (arm64 or amd64) natively; macOS host only
build-darwin-arch arch:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "$(uname -s)" != "Darwin" ]; then
        echo "Error: darwin builds need the Apple SDK and must run on macOS." >&2
        exit 1
    fi
    case "{{arch}}" in
        arm64) clang_arch=arm64 ;;
        amd64) clang_arch=x86_64 ;;
        *) echo "Error: unsupported darwin arch {{arch}}" >&2; exit 1 ;;
    esac
    mkdir -p {{bindir}}/darwin-{{arch}}
    for cmd in cmd/*/; do
        name=$(basename "$cmd")
        CGO_ENABLED=1 CC="clang -arch $clang_arch" MACOSX_DEPLOYMENT_TARGET=12.0 \
          GOOS=darwin GOARCH={{arch}} go build -ldflags "-s -w" -o {{bindir}}/darwin-{{arch}}/$name ./cmd/$name
    done

# Build for Linux (amd64) — statically linked musl binary via zig
build-linux:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p {{bindir}}/linux-amd64
    for cmd in cmd/*/; do
        name=$(basename "$cmd")
        CGO_ENABLED=1 CC="${CC_LINUX_AMD64:-zig cc -target x86_64-linux-musl}" \
          GOOS=linux GOARCH=amd64 go build -tags "netgo osusergo" -ldflags "-s -w -extldflags=-s" -o {{bindir}}/linux-amd64/$name ./cmd/$name
    done

# Build for Linux (arm64 — Docker on Apple Silicon) — statically linked musl binary via zig
build-linux-arm64:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p {{bindir}}/linux-arm64
    for cmd in cmd/*/; do
        name=$(basename "$cmd")
        CGO_ENABLED=1 CC="${CC_LINUX_ARM64:-zig cc -target aarch64-linux-musl}" \
          GOOS=linux GOARCH=arm64 go build -tags "netgo osusergo" -ldflags "-s -w -extldflags=-s" -o {{bindir}}/linux-arm64/$name ./cmd/$name
    done

# Build for Windows (amd64) — self-contained .exe via zig
build-windows:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p {{bindir}}/windows-amd64
    for cmd in cmd/*/; do
        name=$(basename "$cmd")
        CGO_ENABLED=1 CC="${CC_WINDOWS_AMD64:-zig cc -target x86_64-windows-gnu}" \
          GOOS=windows GOARCH=amd64 go build -tags "netgo osusergo" -ldflags "-s -w -extldflags=-s" -o {{bindir}}/windows-amd64/$name.exe ./cmd/$name
    done

# Package the zig-built release archives (the Linux CI job's packaging step)
package: build-release
    cd bin && \
    for platform in linux-amd64 linux-arm64 windows-amd64; do \
        tar -czf "claude-hooks-${platform}.tar.gz" -C "$platform" . ; \
    done
    @echo "Archives created in bin/"
    @ls -lh bin/*.tar.gz

# Package the darwin release archives (the macOS CI job's packaging step)
package-darwin: build-darwin
    cd bin && \
    for platform in darwin-arm64 darwin-amd64; do \
        tar -czf "claude-hooks-${platform}.tar.gz" -C "$platform" . ; \
    done
    @echo "Archives created in bin/"
    @ls -lh bin/claude-hooks-darwin-*.tar.gz

# Run the full CI pipeline locally (test + build-release + package)
ci: test package
    @echo ""
    @echo "CI simulation passed — release platforms built and packaged."
