# Claude Hooks

A collection of hooks and tools for [Claude Code](https://claude.ai/claude-code) that enforce code quality, prevent destructive operations, and automate common tasks.

## Tools

### PreToolUse Hooks

These hooks run before Claude executes a tool, blocking disallowed operations.

| Tool | Description |
|------|-------------|
| [block-destructive-commands](docs/block-destructive-commands.md) | Prevents dangerous CLI commands (rm -rf, git reset --hard, etc.) and blocks destructive git operations |
| [block-generated-files](cmd/block-generated-files/) | Blocks modifications to auto-generated data-layer directories |
| [block-infrastructure](docs/block-infrastructure.md) | Protects critical config files from modification |
| [block-lint-workarounds](docs/block-lint-workarounds.md) | Catches underscore prefixes and suppression comments |
| [docs-tracker](docs/docs-tracker.md) | Enforces documentation reading before code edits |
| [enforce-tests-on-commit](docs/enforce-tests-on-commit.md) | Requires tests for modified source files |

### PostToolUse Hooks

These hooks run after Claude executes a tool, automating follow-up tasks.

| Tool | Description |
|------|-------------|
| [auto-convex-gen](cmd/auto-convex-gen/) | Re-runs convex-gen automatically when Convex source files are edited |
| [format-on-save](cmd/format-on-save/) | Runs Prettier on files after Edit/Write operations |
| [markdown-formatter](docs/markdown-formatter.md) | Auto-formats markdown with code fence language tags |
| [smart-lint](docs/smart-lint.md) | Runs appropriate linters based on project type |
| [smart-test](docs/smart-test.md) | Runs relevant tests after file modifications |
| [track-edited-files](docs/track-edited-files.md) | Tracks source/test file edits per session |

### Code Generation

| Tool | Description |
|------|-------------|
| [convex-gen](docs/convex-gen.md) | Generates React hooks and TypeScript types from Convex functions |

### Pre-commit Checks

| Tool | Description |
|------|-------------|
| [pre-commit](docs/pre-commit.md) | Orchestrates all pre-commit validation checks |
| [changelog-add](docs/changelog-add.md) | Creates changelog fragments with conventional commit format |
| [commit-queue](docs/commit-queue.md) | Runs commits through a machine-wide, first-come-first-served queue so only N pre-commit suites run at once |
| [validate-frontend-structure](docs/validate-frontend-structure.md) | Enforces CRUD folder organization |
| [validate-srp](docs/validate-srp.md) | Validates Single Responsibility Principle |
| [validate-test-files](docs/validate-test-files.md) | Ensures components have required tests |
| [validate-next](docs/validate-next.md) | Validates Next.js public/ asset references and internal links resolve |

## Installation

### From Source

```bash
git clone https://github.com/milehighideas/claude-hooks.git
cd claude-hooks
make build
```

Binaries will be built to the `bin/` directory.

### Install to /usr/local/bin

```bash
make install
```

### Install Individual Tools

```bash
go install github.com/milehighideas/claude-hooks/cmd/smart-lint@latest
```

## Configuration

Add hooks to your Claude Code settings (`~/.claude/settings.json`):

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/claude-hooks/bin/block-destructive-commands"
          }
        ]
      }
    ]
  }
}
```

## Development

```bash
# Run all tests
make test

# Build specific tool
make build-smart-lint

# Clean build artifacts
make clean
```

## License

MIT
