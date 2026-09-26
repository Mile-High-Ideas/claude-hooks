# Pre-commit Hook Tool

**Repository:** [claude-hooks](https://github.com/milehighideas/claude-hooks) (`cmd/pre-commit`)

A comprehensive Go-based pre-commit hook that validates code quality, enforces architectural patterns, and prevents common issues before code is committed. Runs checks in parallel for speed and provides detailed reporting.

## Overview

The pre-commit tool is designed to be used as a Git hook that runs automatically before commits, but can also run standalone for checking arbitrary directories. It performs multiple validation checks simultaneously to catch issues early in the development process.

## Usage

### As a Git Pre-commit Hook (Default)

The tool automatically detects staged files and runs applicable checks:

```bash
pre-commit
```

The tool will:

1. Load configuration from `.pre-commit.json`
2. Identify affected apps and shared packages
3. Run enabled checks in parallel
4. Report failures with actionable errors

### Standalone Mode

Check all files in a directory without Git context:

```bash
pre-commit --standalone --path <directory>
```

Example:

```bash
pre-commit --standalone --path ./apps/mobile
```

### Run Specific Check

Run only one check instead of all enabled checks:

```bash
pre-commit --check <check-name>
```

## Command Line Arguments and Flags

### Primary Flags

- `--standalone` - Run without git context, checking all files in a path
- `--path <directory>` - Directory to check in standalone mode (required with `--standalone`)
- `--check <check-name>` - Run only a specific check by name
- `--list` - List all available checks
- `--config <path>` - Path to `.pre-commit.json` config file (defaults to project root)
- `--report-dir <directory>` - Write detailed analysis reports to this directory

### Examples

```bash
# List all available checks
pre-commit --list

# Run only SRP check
pre-commit --check srp

# Check specific directory standalone
pre-commit --standalone --path ./packages/ui

# Use custom config file
pre-commit --config ./custom-config.json

# Generate detailed reports
pre-commit --report-dir ./analysis-reports
```

## Available Checks

Run `pre-commit --list` to see all available checks. Currently supported:

| Check               | Purpose                                               |
| ------------------- | ----------------------------------------------------- |
| `lintTypecheck`     | ESLint and TypeScript type checking                   |
| `tests`             | Run test suites for affected apps                     |
| `changelog`         | Validate changelog entries exist                      |
| `consoleCheck`      | Check for console.log statements                      |
| `frontendStructure` | Validate CRUD folder structure in components          |
| `srp`               | Single Responsibility Principle validation (TypeScript) |
| `srpNative`         | Structural SRP for Swift/Kotlin (file/type/function length, one type per file) |
| `mockCheck`         | Ensure tests use `__mocks__/` instead of inline mocks |
| `testFiles`         | Ensure test files exist for source files              |
| `vitestAssertions`  | Ensure vitest configs have `requireAssertions: true`  |
| `testCoverage`      | Check source files have corresponding test files      |
| `stubTestCheck`     | Ban `expect(true).toBe(true)` stub tests (per-app scoped) |
| `missingTestsCheck` | Ban source files without co-located `.test.ts(x)` (per-app scoped) |
| `goLint`            | Go linting (when enabled)                             |
| `convexValidation`  | Convex functions compile (when enabled) — see [modes](#convex-validation-modes) |
| `buildCheck`        | Build verification (when enabled)                     |
| `nextImageCheck`    | Verify Next.js `public/` asset references resolve (static) |
| `nextLinkCheck`     | Verify Next.js internal links resolve (static / crawl / both) |

See [validate-next](validate-next.md) for the Next.js checks, which are also
available as a standalone binary.


## Convex validation modes

`convexValidation` runs the project's installed Convex CLI. `convex.mode`
chooses how:

| mode | command | touches the deployment? |
| --- | --- | --- |
| `codegen` (default) | `convex codegen --typecheck enable` | **No.** The CLI documents it as "This doesn't modify the code running on the deployment." |
| `dev` | `convex dev --once` | **Yes — it deploys.** |

```jsonc
"convex": {
  "path": "packages/backend",
  "mode": "codegen"  // omit for the same thing
}
```

**`dev` mode deploys.** Step 2 of `convex dev` is documented as "pushes code to
the configured dev deployment", and `--once` includes it. The deployment is
whatever `CONVEX_DEPLOY_KEY` names — so if that points at production,
committing is a production deploy. Worse, the push happens inside the check:
a commit rejected by a later gate can still have deployed, leaving code serving
production with no commit to point at.

This was the default until Mile-High-Ideas/claude-hooks#3. It is retained for
anyone who wants server-side push validation — whether the deployment accepts
these indexes and schema — and accepts that cost.

`codegen` keeps schema parsing, code generation, component bundling and
TypeScript. It is also the stricter typecheck: `convex dev` defaults
`--typecheck` to `try`, which silently skips when tsc cannot run, so the `dev`
path can pass without typechecking anything.

`successMarker` applies only to `dev`. `codegen` prints no completion line and
is judged on exit status.

### Production guard

Before either mode runs, the check refuses to validate against a production
deployment. It looks at `CONVEX_DEPLOY_KEY` and `CONVEX_DEPLOYMENT` in the
process environment and in `.env.local` / `.env` under `convex.path` (the files
the Convex CLI loads), and blocks the commit when either starts with `prod:`.

The refusal names the variable and the file it came from, never its value, and
tells the developer to run `just convex-local-setup` to point the checkout at a
local deployment. `anonymous:`, `local:`, `dev:` and `preview:` values pass.

Why: `codegen` is read-only, but it still needs a credential, and when the only
credential in a checkout is the production deploy key, every worktree and agent
session that commits ends up holding it. A `"mode": "dev"` typo with that key in
place is a production deploy.

The guard is on by default. To turn it off:

```jsonc
"convex": {
  "path": "packages/backend",
  "refuseProductionDeployment": false
}
```

## Configuration

The tool uses a `.pre-commit.json` file in the project root. Create this file to enable and configure checks.

### Configuration File Structure

```json
{
  "packageManager": "pnpm",
  "env": {
    "NODE_OPTIONS": "--max-old-space-size=8192"
  },
  "apps": {
    "web": {
      "path": "apps/web",
      "filter": "@myorg/web",
      "testCommand": "test",
      "nodeMemoryMB": 8192
    },
    "mobile": {
      "path": "apps/mobile",
      "filter": "@myorg/mobile"
    }
  },
  "sharedPaths": ["packages/", "tsconfig.json", ".eslintrc"],
  "reportDir": "./analysis-reports",
  "features": {
    "lintTypecheck": true,
    "lintStaged": true,
    "fullLintOnCommit": false,
    "tests": true,
    "changelog": true,
    "consoleCheck": true,
    "branchProtection": false,
    "goLint": false,
    "convexValidation": false,
    "buildCheck": false,
    "frontendStructure": true,
    "srp": true,
    "fullSRPOnCommit": false,
    "testFiles": true,
    "mockCheck": true,
    "vitestAssertions": true,
    "testCoverage": true,
    "stubTestCheck": true,
    "missingTestsCheck": true
  },
  "protectedBranches": ["main", "production"],
  "changelogExclude": ["^docs/", "^README"],
  "changelog": {
    "mode": "global",
    "globalDir": ".changelog",
    "apps": []
  },
  "consoleAllowed": ["scripts/", "cli/"],
  "typecheckFilter": {
    "errorCodes": ["TS2589", "TS2742"],
    "excludePaths": ["__tests__/", ".test.", ".spec."],
    "skipLibCheck": true,
    "useBuildMode": false
  },
  "lintFilter": {
    "rules": [],
    "excludePaths": ["__tests__/", ".test."],
    "linter": "eslint"
  },
  "lintStagedConfig": {
    "packageManager": "pnpm",
    "env": {
      "COREPACK_ENABLE_STRICT": "0"
    }
  },
  "goLint": {
    "paths": ["./pre-commit", "./other-go-packages"],
    "tool": "golangci-lint"
  },
  "convex": {
    "path": "apps/backend/convex",
    "successMarker": "Convex functions ready!"
  },
  "build": {
    "apps": ["web", "mobile"]
  },
  "mockCheck": {
    "enforceDirectory": true
  },
  "testConfig": {
    "affectedOnly": false,
    "runOnSharedChanges": true,
    "appOverrides": {
      "web": {
        "enabled": true,
        "onlyWhenAffected": true
      },
      "mobile": {
        "enabled": false
      }
    }
  },
  "testCoverageConfig": {
    "appPaths": ["apps/portal", "apps/mobile"],
    "requireTestFolders": [
      "hooks",
      "read",
      "create",
      "update",
      "delete",
      "utils"
    ],
    "excludeFiles": ["index.ts", "*.types.ts"],
    "excludePaths": ["__tests__/", "fixtures/"]
  },
  "srpConfig": {
    "appPaths": ["apps/portal", "apps/mobile"],
    "excludePaths": ["data-layer/", "providers/"],
    "hideWarnings": false
  },
  "stubTestCheckConfig": {
    "mode": "all",
    "appPaths": ["apps/portal", "packages/ui"],
    "excludePaths": ["apps/portal/legacy"]
  }
}
```

#### Stub Test Check (`stubTestCheck`)

Detects test files whose every `expect()` call is the `expect(true).toBe(true)` placeholder — tests that look like tests but verify nothing. Shares its detector with `validate-test-files`, so the hook that prevents new stubs at Write/Edit time and this commit-time gate can never drift.

```jsonc
"stubTestCheckConfig": {
  // "all" (default) — walk appPaths and fail on ANY stub, including files
  // this commit didn't touch. Use this as a "ratchet": once an app is
  // stub-free, enabling it locks in that state forever.
  //
  // "staged" — only flag test files currently staged. Lighter-weight;
  // redundant with the validate-test-files PreToolUse hook but useful
  // as a belt-and-suspenders commit gate.
  "mode": "all",

  // Substring match on project-relative path. Empty = scan entire project.
  "appPaths": ["apps/portal", "packages/ui"],

  // Always wins over appPaths. Substring match.
  "excludePaths": ["apps/portal/legacy"]
}
```

Progressive cleanup: start with `appPaths: []` (nothing enforced), clean one app, add it to `appPaths`. Repeat until every app is in the list. Same idiom the `-list-stubs` flag on `validate-test-files` supports for auditing.

#### Missing Tests Check (`missingTestsCheck`)

Detects source files that have no co-located test — the symmetric counterpart to `stubTestCheck`. Same shape, same semantics:

```jsonc
"missingTestsCheckConfig": {
  // "all" (default) — walk appPaths and fail on ANY source file that
  //   lacks a *.test.ts(x). Ratchet: once clean, stays clean.
  // "staged" — only flag staged source files missing tests. Lighter
  //   weight; overlaps with enforce-tests-on-commit but keeps all the
  //   test-related gates in one binary.
  "mode": "all",

  // Substring match on project-relative path. Empty = scan entire project.
  "appPaths": ["packages/backend", "apps/web"],

  // Always wins over appPaths.
  "excludePaths": ["packages/backend/convex/_generated"]
}
```

Files that are automatically skipped (never require a test, regardless of scope):

- Test files (`.test.ts[x]`, `.spec.ts[x]`, `.e2e.ts[x]`, `.maestro.yaml`)
- Barrels (`index.ts`, `index.tsx`)
- Type definitions (`.d.ts`, `.types.ts[x]`, anything under `/types/`)
- Layout files (`_layout.ts[x]`)
- Config files (`*.config.ts[x]`)
- Anything under `node_modules`, `.git`, `_generated`, `dist`, `build`, `.next`, `.turbo`, `.vercel`, `__mocks__`, `__tests__`

A `.spec.ts(x)` sibling also satisfies the check — a source file with `Foo.spec.tsx` next to it counts as tested even if no `Foo.test.tsx` exists.

### Key Configuration Options

#### Global Options

- **packageManager**: `pnpm` (default), `bun`, `npm`, `yarn`
- **env**: Environment variables passed to all commands
- **reportDir**: Directory for detailed analysis reports (organized by check type)

#### Apps Configuration

Each app requires:

- **path**: Filesystem path to the app
- **filter**: Package manager filter name (for `pnpm --filter`)
- **testCommand** (optional): Custom test script name (default: `test`)
- **nodeMemoryMB** (optional): Memory limit for Node.js processes
- **typecheckFilter** (optional): Per-app typecheck overrides

#### Features

Enable/disable checks with boolean flags. Some checks support extended configuration (see sections below).

#### Type Checking Configuration

```json
"typecheckFilter": {
  "errorCodes": ["TS2589", "TS2742"],
  "excludePaths": ["__tests__/", ".test."],
  "skipLibCheck": true,
  "useBuildMode": false
}
```

- **errorCodes**: TypeScript error codes to filter out globally
- **excludePaths**: File paths to exclude from type checking
- **skipLibCheck**: Skip checking `.d.ts` files (default: true for compatibility)
- **useBuildMode**: Use `tsc -b` instead of `tsc --noEmit` (default: false)

#### Linting Configuration

```json
"lintFilter": {
  "rules": [],
  "excludePaths": ["__tests__/"],
  "linter": "eslint"
}
```

- **linter**: `eslint` (default) or `oxlint`
- **rules**: ESLint rule names to filter out
- **excludePaths**: Files to exclude from linting

#### Changelog Configuration

```json
"changelog": {
  "mode": "global|per-app|required",
  "globalDir": ".changelog",
  "apps": []
}
```

- **global**: Single `.changelog/` directory at project root
- **per-app**: Each app has its own `.changelog/`, with fallback to global for shared changes
- **required**: Each affected app must have its own changelog (no global fallback)

#### SRP (Single Responsibility Principle) Configuration

```json
"srpConfig": {
  "appPaths": ["apps/portal", "apps/mobile"],
  "excludePaths": ["data-layer/", "providers/"],
  "hideWarnings": false
}
```

- **appPaths**: Limit SRP checks to specific app directories
- **excludePaths**: Paths to exclude from SRP checking
- **hideWarnings**: Don't show warnings, only errors

#### SRP Native Configuration

```json
"srpNativeConfig": {
  "appPaths": ["apps/maps-ios", "apps/maps-android"],
  "excludePaths": [],
  "fileLines": 400,
  "typeBodyLines": 300,
  "funcBodyLines": 60,
  "minTypeBodyLines": 40,
  "oneTypeIgnoreConformances": ["PreviewProvider"],
  "fileLinesOverrides": {}
}
```

- **appPaths**: Limit native SRP checks to these directories (empty = all)
- **excludePaths**: Path substrings to skip. Build artifacts and
  SwiftPM/Pods/Gradle checkouts (`build/`, `build-device/`, `.build/`,
  `DerivedData/`, `SourcePackages/`, `Pods/`, `.gradle/`) are **always**
  excluded on top of this list.
- **swiftExtensions** / **kotlinExtensions**: Override the grammar-by-extension
  mapping (defaults `[".swift"]` / `[".kt", ".kts"]`)
- **fileLines** / **typeBodyLines** / **funcBodyLines**: Line limits (defaults
  400 / 300 / 60)
- **minTypeBodyLines**: Body-size floor for `oneTypePerFile` (default 40) — a
  top-level type only counts toward the one-type-per-file limit if its body is
  at least this many lines
- **oneTypeIgnoreConformances**: Protocols whose conformers are exempt from
  `oneTypePerFile` (default `["PreviewProvider"]`)
- **fileLinesOverrides**: Map of path-substring → per-file line limit
- **enabledRules**: Subset of `fileSize`, `typeBodyLength`, `functionBodyLength`,
  `oneTypePerFile` to run (empty = all four)

#### Test Configuration

```json
"testConfig": {
  "affectedOnly": false,
  "runOnSharedChanges": true,
  "appOverrides": {
    "web": {
      "enabled": true,
      "onlyWhenAffected": true
    }
  }
}
```

- **affectedOnly**: Only run tests for apps with staged changes
- **runOnSharedChanges**: Run all tests when shared paths change (default: true)
- **appOverrides**: Per-app test configuration
  - **enabled**: Override global tests flag for this app
  - **onlyWhenAffected**: Run tests only when this app is affected

## Environment Variables

### Global Environment Variables

Set in the `env` section of config, passed to all commands:

```json
"env": {
  "NODE_OPTIONS": "--max-old-space-size=8192",
  "SOME_API_KEY": "value"
}
```

### Lint-Staged Environment Variables

Set in `lintStagedConfig.env`:

```json
"lintStagedConfig": {
  "env": {
    "COREPACK_ENABLE_STRICT": "0"
  }
}
```

### Pre-commit Specific Variables

- `SKIP_CHANGELOG_CHECK=1` - Skip changelog validation (useful for automated commits)
- `NODE_OPTIONS` - Set Node.js memory limits (e.g., `--max-old-space-size=8192`)

## Exit Codes

- **0** - All checks passed
- **1** - One or more checks failed
  - Check output will show which checks failed
  - Detailed reports written to `--report-dir` if specified

## Example Usage Patterns

### Basic Setup

1. Create `.pre-commit.json`:

```json
{
  "packageManager": "pnpm",
  "apps": {
    "web": {
      "path": "apps/web",
      "filter": "@myorg/web"
    }
  },
  "sharedPaths": ["packages/", "tsconfig.json"],
  "features": {
    "lintTypecheck": true,
    "tests": true
  }
}
```

2. Run checks:

```bash
pre-commit
```

### Monorepo with Multiple Apps

```json
{
  "packageManager": "pnpm",
  "apps": {
    "web": {
      "path": "apps/web",
      "filter": "@myorg/web",
      "nodeMemoryMB": 8192
    },
    "mobile": {
      "path": "apps/mobile",
      "filter": "@myorg/mobile"
    },
    "admin": {
      "path": "apps/admin",
      "filter": "@myorg/admin",
      "testCommand": "test:ci"
    }
  },
  "sharedPaths": ["packages/", "infra/"],
  "features": {
    "lintTypecheck": true,
    "tests": true,
    "changelog": true,
    "srp": true,
    "frontendStructure": true
  }
}
```

### Skip Tests for Specific App

```json
{
  "features": {
    "tests": true
  },
  "testConfig": {
    "appOverrides": {
      "admin": {
        "enabled": false
      }
    }
  }
}
```

### Run Full Checks on Every Commit

```json
{
  "features": {
    "fullLintOnCommit": true,
    "fullSRPOnCommit": true
  }
}
```

### Generate Analysis Reports

```bash
pre-commit --report-dir ./reports
```

**Every enabled check writes a report** so any failure during a commit is
inspectable. Reports are organized by check type, and each report is a pair:

- `fullreport.txt` — written **always** (pass or fail): the check's complete
  output (summary counts, every section, raw linter/tsc/command output), or a
  `✅ <Check> passed` banner on a clean run.
- `findings.txt` — written **only on failure**: just the blocking, actionable
  items. For lint/typecheck the surviving findings with the raw tool dump and
  filtered-out noise stripped; for SRP the blocking errors only; for
  command-based checks (native build, bundle, convex) the error/failure lines.

Checks with a per-app dimension nest the pair under `<app>/`; aggregate checks
write it flat:

```text
reports/branch-name_timestamp/
  lint/<app>/{findings,fullreport}.txt            - per app
  typecheck/<app>/{findings,fullreport}.txt       - per app
  srp/<app>/{findings,fullreport}.txt             - per app
  console-check/<app>/{findings,fullreport}.txt   - per app
  native-build/<app>/{findings,fullreport}.txt    - per app (+ flat aggregate)
  bundle-check/<app>/{findings,fullreport}.txt    - per app (+ flat aggregate)
  convex-validation/{findings,fullreport}.txt     - flat
  frontend-structure/{findings,fullreport}.txt    - flat
  convex-check/{findings,fullreport}.txt          - flat
  next-image/{findings,fullreport}.txt            - flat
  next-link/{findings,fullreport}.txt             - flat
  test-files/{findings,fullreport}.txt            - flat
  test-coverage/ test-quality/ vitest-assertions/ - flat
  ...
```

Checks **disabled** in `.pre-commit.json` don't run, so they write nothing. The
runner prints a `→ reports/.../<check>/` pointer beneath any failed check.

## Check Details

### Lint & Type Checking

Runs ESLint and TypeScript checks in parallel for all affected apps.

**Filtering**:

- Filter specific TypeScript error codes (TS2589, TS2742 by default)
- Filter specific ESLint rules
- Exclude specific file patterns

**Modes**:

- **Incremental**: Only check staged files (default)
- **Full**: Check entire app (enabled with `fullLintOnCommit`)

### Tests

Runs test suites based on configuration.

**Behavior**:

- Defaults to all apps unless `affectedOnly` is true
- Can specify custom test command per app
- Skips shared test runs if no shared paths changed

### SRP (Single Responsibility Principle)

Validates code organization and architecture:

- No direct Convex imports outside data-layer
- Screens/pages have no state management
- Single export per file in CRUD folders
- File size limits (screens: 100 lines, hooks: 150 lines, components: 200 lines)
- Types in dedicated `types/` folders
- No mixed concerns (data fetching + UI + state)

### Native SRP (`srpNative`)

Structural SRP for native source — Swift (iOS) and Kotlin (Android) — using the
same tree-sitter engine as `srp`, in the shared `internal/srpnative` analyzer.
The TypeScript-only `srp` rules (Convex imports, screen state, type-export
location, mixed concerns) do not apply; instead four size/structure rules run:

- `fileSize` — total file lines over the limit (default 400)
- `typeBodyLength` — a single class/struct/enum/actor body over the limit
  (default 300) — the Massive-View-Controller catcher
- `functionBodyLength` — a single function/method body over the limit (default 60)
- `oneTypePerFile` — more than one **substantial** top-level type in a file.
  A type only counts if its body ≥ `minTypeBodyLines` (default 40) and it does
  not conform to an ignored protocol (default `PreviewProvider`), so idiomatic
  co-location (a parent SwiftUI view with small private subviews, grouped
  response DTOs, an Xcode preview struct) is not flagged. Swift extensions and
  nested types never count.

All violations are errors. Scoping is **staged native files only** — there is no
warnOnly/grandfather path. Build artifacts and SwiftPM/Pods/Gradle checkouts are
always excluded on top of `excludePaths`. Configuration lives in
`srpNativeConfig` (see [SRP Native Configuration](#srp-native-configuration)).

### Console Check

Detects `console.log`, `console.warn`, `console.error`, etc.

**Configuration**:

- `consoleAllowed`: File patterns where console statements are permitted (e.g., scripts, CLI)

### Frontend Structure

Validates CRUD folder structure in component directories (create, read, update, delete folders with proper organization).

### Changelog Validation

Enforces changelog entries for commits.

**Modes**:

- **global**: Single `.changelog/` at root
- **per-app**: Each app has `.changelog/`, global fallback for shared changes
- **required**: Each affected app must have changelog (no global fallback)

**Skip temporarily**:

```bash
SKIP_CHANGELOG_CHECK=1 git commit -m "message"
```

### Mock Check

Ensures test files use `__mocks__/` directory for mocks instead of inline `jest.mock()` calls.

### Test Files

Ensures corresponding test files exist for source files.

### Vitest Assertions

Validates that vitest config files have `requireAssertions: true` to prevent empty test suites.

### Go Linting

Runs Go linter on specified paths (golangci-lint by default).

### Convex Validation

Validates Convex backend schema and generates updated type definitions.

### Build Check

Verifies specified apps can build successfully.

## Parallel Execution

The tool runs checks in parallel for performance:

- Multiple apps' lint/typecheck run concurrently
- Tests for different apps run concurrently
- Independent checks run simultaneously

Reports are written sequentially after all jobs complete to avoid race conditions.

## Output

### During Execution

```bash
Running pre-commit checks...

Detected 2 changed file(s) in web app
Detected 1 changed file(s) in mobile app

================================
  LINT & TYPE CHECKING (PARALLEL)
================================

Running checks on 2 app(s) in parallel...

🔍 Running incremental checks for web (5 files)...
   ✓ web passed typecheck
   ✓ web passed lint
✅ web passed all checks

🔍 Running incremental checks for mobile (3 files)...
   ✓ mobile passed typecheck
   ✓ mobile passed lint
✅ mobile passed all checks

================================
  ALL PRE-COMMIT CHECKS PASSED!
================================
```

### On Failure

```text
❌ Found 2 lint error(s)

apps/web/src/Button.tsx
  5:10  error  'unused'  @typescript-eslint/no-unused-vars
  15:5  error  Unexpected any  @typescript-eslint/no-explicit-any

================================
  PRE-COMMIT CHECKS FAILED
================================

Fix the errors above and try again
```

## Performance Optimization

### Tips for Faster Checks

1. **Use incremental checking** (default):
   - Only processes staged files instead of entire app
   - Set `fullLintOnCommit: false`

2. **Configure type checking**:
   - Set `skipLibCheck: true` to skip `.d.ts` file checks
   - Use `useBuildMode: true` only when needed

3. **Memory settings**:
   - Set `nodeMemoryMB` per app if experiencing out-of-memory
   - Example: `"nodeMemoryMB": 8192` for 8GB limit

4. **Run specific checks**:
   - Use `--check` flag to test individual checks during development
   - Example: `pre-commit --check lintTypecheck`

## Troubleshooting

### "pnpm not found"

Make sure pnpm (or your configured package manager) is in your PATH.

### Lint/typecheck timeouts

Set memory limits in config:

```json
{
  "env": {
    "NODE_OPTIONS": "--max-old-space-size=8192"
  }
}
```

Or per-app:

```json
{
  "apps": {
    "web": {
      "path": "apps/web",
      "nodeMemoryMB": 8192
    }
  }
}
```

### Tests skipped unexpectedly

Check your test configuration:

- Is `tests` feature enabled?
- If `affectedOnly: true`, is the app actually affected by staged changes?
- Check per-app overrides in `testConfig.appOverrides`

### Changelog check failing

Make sure:

- Changelog mode is correctly configured
- Changelog directory exists (`.changelog/` by default)
- Fragment files have `.txt` extension

## Integration with Git Hooks

Install as a pre-commit hook:

```bash
# Copy to .git/hooks/
cp pre-commit .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
```

Or use a Git hooks manager like `husky`:

```bash
npx husky add .husky/pre-commit "pre-commit"
```

## Related Tools

- **changelog-add**: Add changelog fragments (used with changelog feature)
- **lint-staged**: Automatic code formatting (runs before pre-commit checks)
- **eslint**: JavaScript/TypeScript linting
- **tsc**: TypeScript type checking
- **vitest**: JavaScript test runner
- **pnpm**: Package manager (supports `--filter` for monorepos)
