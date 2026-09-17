package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/milehighideas/claude-hooks/internal/jsonc"
)

// Config represents the .pre-commit.json configuration
type Config struct {
	PackageManager                string                        `json:"packageManager"` // Global package manager: "pnpm" (default), "bun", "npm", "yarn"
	Env                           map[string]string             `json:"env"`            // Global environment variables for all commands
	Apps                          map[string]AppConfig          `json:"apps"`
	SharedPaths                   []string                      `json:"sharedPaths"`
	ReportDir                     string                        `json:"reportDir"` // Directory to write analysis reports
	Features                      Features                      `json:"features"`
	ProtectedBranches             []string                      `json:"protectedBranches"`
	ChangelogExclude              []string                      `json:"changelogExclude"`
	ChangelogConfig               ChangelogConfig               `json:"changelog"`
	ConsoleAllowed                []string                      `json:"consoleAllowed"`
	TypecheckFilter               TypecheckFilter               `json:"typecheckFilter"`
	LintFilter                    LintFilter                    `json:"lintFilter"`
	LintStagedConfig              LintStagedConfig              `json:"lintStagedConfig"`
	GoLint                        GoLintConfig                  `json:"goLint"`
	GoMissingTestsCheck           GoMissingTestsCheckConfig     `json:"goMissingTestsCheckConfig"`
	GoTests                       GoTestsConfig                 `json:"goTests"`
	NativeBuild                   NativeBuildConfig             `json:"nativeBuild"`
	Convex                        ConvexConfig                  `json:"convex"`
	Build                         BuildConfig                   `json:"build"`
	BundleCheck                   BundleCheckConfig             `json:"bundleCheckConfig"`
	MockCheck                     MockCheckConfig               `json:"mockCheck"`
	TestConfig                    TestConfig                    `json:"testConfig"`
	TestCoverageConfig            TestCoverageConfig            `json:"testCoverageConfig"`
	TestQualityConfig             TestQualityConfig             `json:"testQualityConfig"`
	SRPConfig                     SRPConfig                     `json:"srpConfig"`
	SRPNativeConfig               SRPNativeConfig               `json:"srpNativeConfig"`
	DataLayerAllowed              []string                      `json:"dataLayerAllowed"`
	NextImageCheck                NextImageCheckConfig          `json:"nextImageCheck"`
	NextLinkCheck                 NextLinkCheckConfig           `json:"nextLinkCheck"`
	MaestroValidation             MaestroValidationConfig       `json:"maestroValidation"`
	StubTestCheckConfig           StubTestCheckConfig           `json:"stubTestCheckConfig"`
	MissingTestsCheckConfig       MissingTestsCheckConfig       `json:"missingTestsCheckConfig"`
	TestSubstanceCheckConfig      TestSubstanceCheckConfig      `json:"testSubstanceCheckConfig"`
	RedundantCreatedAtCheckConfig RedundantCreatedAtCheckConfig `json:"redundantCreatedAtCheckConfig"`
	WarningChecks                 []string                      `json:"warningChecks"` // Checks listed here run but don't block commits
}

// RedundantCreatedAtCheckConfig configures the Convex schema `createdAt`
// detector. Convex automatically maintains `_creationTime: number` on every
// row and exposes a `by_creation_time` index for free, so a custom
// `createdAt` column inside `defineTable({...})` duplicates data and risks
// drift when callers pass a different value.
type RedundantCreatedAtCheckConfig struct {
	// Mode controls what's scanned.
	// "all" (default) — walks AppPaths (or project root if empty) and
	// reports every Convex schema file currently containing a redundant
	// createdAt. Use this as a ratchet: once a package is clean, locking
	// it in prevents regression.
	// "staged" — only checks schema files currently staged for commit.
	// Lighter; lets you adopt incrementally without failing on legacy
	// schemas.
	Mode string `json:"mode"`
	// StatusFilter narrows which staged files are checked when Mode is
	// "staged". "all" (default) checks every staged schema file (Added,
	// Copied, Modified, Renamed). "added" only checks newly added files,
	// so a mechanical bulk refactor (e.g. a package rename) can move
	// existing schema files without re-flagging their pre-existing,
	// grandfathered createdAt fields. The edit-time block-redundant-createdat
	// hook still blocks NEW createdAt fields at Write time.
	StatusFilter string `json:"statusFilter"`
	// AppPaths restricts scanning to files whose project-relative path
	// contains at least one of these substrings. Empty = scan the whole
	// project. For this check the useful value is typically
	// `["packages/backend"]` or similar.
	AppPaths []string `json:"appPaths"`
	// ExcludePaths skips files whose project-relative path contains any of
	// these substrings. Exclusions always win over AppPaths.
	ExcludePaths []string `json:"excludePaths"`
}

// ConvexCheckConfig configures the convexCheck feature: it runs the
// @milehighideas/oxlint-plugin-convex rules on staged Convex files and blocks
// on the rules listed in ErrorRules. Thresholds (maxLines/maxFunctions/
// crudDomains) are read by the oxlint plugin itself from this same .pre-commit
// block; this struct is the Go side's view.
type ConvexCheckConfig struct {
	// AppPaths restricts to files whose path contains one of these. Empty =
	// no convex files matched (feature effectively off).
	AppPaths []string `json:"appPaths"`
	// ExcludePaths skips files whose path contains any of these substrings.
	ExcludePaths []string `json:"excludePaths"`
	// ErrorRules is the set of convex rule ids promoted to error on
	// changed/edited files (the ratchet). Empty = dormant (nothing blocks).
	// `.oxlintrc.json` stays at "warn" so fullLint/CI never block on existing
	// violations; enforcement is opt-in per rule here. e.g.
	// ["type-exports-location", "no-any-returns"]. crud-structure additionally
	// self-gates to convexCheckConfig.crudDomains inside the plugin.
	ErrorRules []string `json:"errorRules"`
	// EslintErrorRules is the set of @convex-dev/eslint-plugin rule ids (bare,
	// e.g. "explicit-table-ids") — the type-aware rules oxlint can't do —
	// enforced on staged convex files via eslint_d. Empty = skip the eslint
	// pass. Best-effort: silently skipped if eslint_d isn't installed.
	EslintErrorRules []string `json:"eslintErrorRules"`
}

// MissingTestsCheckConfig configures the missing-tests detector. Same shape
// as stubTestCheckConfig for symmetry: a mode field plus per-app scope,
// with optional per-app overrides.
type MissingTestsCheckConfig struct {
	// Mode is the default mode applied to every entry in AppPaths that
	// doesn't have a per-app override in AppModes. "all" (default) walks
	// the path recursively and reports every source file without a
	// co-located .test.ts(x). "staged" only checks staged source files.
	// Use "all" as a ratchet once a package is test-covered.
	Mode string `json:"mode"`
	// StatusFilter narrows which staged files are checked when Mode is
	// "staged". "all" (default) checks every staged file (Added, Copied,
	// Modified, Renamed). "added" only checks newly added files, letting
	// mechanical bulk refactors of existing untested code pass through.
	StatusFilter string `json:"statusFilter"`
	// AppPaths restricts scanning to files whose project-relative path
	// contains at least one of these substrings. Empty = scan the whole
	// project under the global Mode.
	AppPaths []string `json:"appPaths"`
	// AppModes is a per-path mode override map. Paths listed here are
	// automatically in scope. When a path appears in both AppModes and
	// AppPaths, AppModes wins. Use this to ratchet specific packages on
	// "all" while keeping the rest on "staged".
	AppModes map[string]string `json:"appModes"`
	// ExcludePaths skips files whose project-relative path contains any of
	// these substrings. Exclusions always win over AppPaths/AppModes.
	ExcludePaths []string `json:"excludePaths"`
}

// TestSubstanceCheckConfig configures the test-substance gates: LOC ratio,
// UI-interaction requirement, branch-proportional it() block count, and
// tautological-assertion detection. Runs against the (source, test) pair
// for each staged source file with a co-located test. The intent is to
// catch "minimal tests" — tests that exist on disk and pass lint but
// don't actually exercise the code they claim to cover.
type TestSubstanceCheckConfig struct {
	// Mode is "staged" (default) or "all". Staged checks only the test
	// files paired with currently staged source files. "all" walks every
	// app path recursively (use as a ratchet once a package is clean).
	Mode string `json:"mode"`
	// AppPaths restricts the scan to files under at least one of these
	// project-relative substrings. Mirrors missingTestsCheckConfig.appPaths.
	AppPaths []string `json:"appPaths"`
	// ExcludePaths skips files whose path contains any of these substrings.
	// Exclusions always win over AppPaths.
	ExcludePaths []string `json:"excludePaths"`
	// MinTestSourceRatio is the minimum (testLOC / sourceLOC) ratio. The
	// underlying counter ignores blanks and comments. 0 disables the LOC
	// gate. Default: 0.3 (test must be at least 30% of source LOC).
	MinTestSourceRatio float64 `json:"minTestSourceRatio"`
	// BranchToItRatio is the maximum sourceBranches / testItBlocks ratio.
	// 0 disables the branch gate. Default: 4 (one it() per 4 branches; a
	// 14-branch source needs at least 4 it()s).
	BranchToItRatio int `json:"branchToItRatio"`
	// RequireInteraction enables the UI-interaction gate when the source
	// looks like a React component. Default: true.
	RequireInteraction *bool `json:"requireInteraction"`
	// MinSourceLOCForCheck is the LOC floor below which the LOC-ratio and
	// branch gates are skipped (small utility files don't need ratio
	// enforcement). Default: 50.
	MinSourceLOCForCheck int `json:"minSourceLOCForCheck"`
	// AllowTautological controls whether tautological assertions
	// (expect(X).toBe(X) where X==Y syntactically) are allowed. Default:
	// false (tautologies are violations).
	AllowTautological bool `json:"allowTautological"`
}

// StubTestCheckConfig configures the stub-test detector. Mirrors the shape of
// other scoped configs (srpConfig, testCoverageConfig, etc).
type StubTestCheckConfig struct {
	// Mode is the default mode applied to every entry in AppPaths that
	// doesn't have a per-app override in AppModes. Default "all" — walks the
	// path recursively and reports every stub file. "staged" — only checks
	// staged test files living under that path. Use "all" when you want a
	// ratchet: once an app is stub-free, locking it in prevents regression.
	Mode string `json:"mode"`
	// StatusFilter narrows which staged files are checked when Mode is
	// "staged". "all" (default) checks every staged file (Added, Copied,
	// Modified, Renamed). "added" only checks newly added files, letting
	// mechanical bulk refactors of existing stubs pass through.
	StatusFilter string `json:"statusFilter"`
	// AppPaths restricts scanning to files whose project-relative path
	// contains at least one of these substrings. Empty = scan the whole
	// project under the global Mode.
	AppPaths []string `json:"appPaths"`
	// AppModes is a per-path mode override map, e.g.
	// {"apps/admin": "all", "apps/mobile": "staged"}. Paths listed here are
	// automatically in scope (no need to also list in AppPaths). When a path
	// appears in both AppModes and AppPaths, AppModes wins. Use this to
	// ratchet specific apps (mode "all") while keeping the rest on "staged".
	AppModes map[string]string `json:"appModes"`
	// ExcludePaths skips files whose project-relative path contains any of
	// these substrings. Exclusions always win over AppPaths/AppModes.
	ExcludePaths []string `json:"excludePaths"`
}

// IsWarningCheck returns true if the named check should warn instead of block.
func (c *Config) IsWarningCheck(name string) bool {
	for _, w := range c.WarningChecks {
		if w == name {
			return true
		}
	}
	return false
}

// Features represents which pre-commit features are enabled
type Features struct {
	// Lint and Typecheck are independent phases. A previous flag `lintTypecheck`
	// ran both together; it was split so each can be toggled, gated, and
	// reported on separately. Keep these two flags in sync in .pre-commit.json
	// if you want the old combined behavior.
	Lint                bool `json:"lint"`
	Typecheck           bool `json:"typecheck"`
	LintStaged          bool `json:"lintStaged"`
	FullLintOnCommit    bool `json:"fullLintOnCommit"`
	Tests               bool `json:"tests"`
	Changelog           bool `json:"changelog"`
	ConsoleCheck        bool `json:"consoleCheck"`
	BranchProtection    bool `json:"branchProtection"`
	GoLint              bool `json:"goLint"`
	GoMissingTestsCheck bool `json:"goMissingTestsCheck"`
	GoTests             bool `json:"goTests"`
	ConvexValidation    bool `json:"convexValidation"`
	BuildCheck          bool `json:"buildCheck"`
	// BundleCheck runs Metro/Webpack bundle-only validation per app
	// (typically `expo export` for RN apps). Catches missing-dependency and
	// bad-import errors that pass typecheck and lint but break the bundler.
	// See bundleCheckConfig for which apps to bundle and the npm script name.
	BundleCheck       bool `json:"bundleCheck"`
	FrontendStructure bool `json:"frontendStructure"`
	SRP               bool `json:"srp"`
	FullSRPOnCommit   bool `json:"fullSRPOnCommit"`
	// SrpStrictOnStaged makes SRP block the commit on any violation in a
	// staged file (git diff-filter=ACMR), regardless of warnOnly. The
	// full-repo audit and per-app report are unaffected — they keep
	// surfacing legacy debt as warnings. Equivalent to setting
	// srpConfig.errorScopes: ["changed"] but expressed as a single feature
	// flag for the common "ratchet new work, audit existing code" workflow.
	SrpStrictOnStaged bool `json:"srpStrictOnStaged"`
	// SrpNative runs structural SRP checks (file/type/function length, one type
	// per file) on staged Swift and Kotlin files. Configured via srpNativeConfig.
	// Hard-error policy: every violation blocks; scoping is staged-files only.
	SrpNative               bool `json:"srpNative"`
	TestFiles               bool `json:"testFiles"`
	MockCheck               bool `json:"mockCheck"`
	VitestAssertions        bool `json:"vitestAssertions"`
	TestCoverage            bool `json:"testCoverage"`
	TestQuality             bool `json:"testQuality"`
	NativeBuild             bool `json:"nativeBuild"`
	DataLayerCheck          bool `json:"dataLayerCheck"`
	MaestroValidation       bool `json:"maestroValidation"`
	StubTestCheck           bool `json:"stubTestCheck"`
	MissingTestsCheck       bool `json:"missingTestsCheck"`
	RedundantCreatedAtCheck bool `json:"redundantCreatedAtCheck"`
	ConvexCheck             bool `json:"convexCheck"`
	TiersGen                bool `json:"tiersGen"`
	LinguiExtract           bool `json:"linguiExtract"`
	// TestSubstanceCheck runs substance gates against test files for staged
	// source files: LOC ratio, UI-interaction requirement, branch-proportional
	// it() count, plus tautological-assertion detection. Catches "minimal
	// tests" that satisfy the missingTestsCheck file-existence gate but don't
	// actually exercise the source. Configured via testSubstanceCheckConfig.
	TestSubstanceCheck bool `json:"testSubstanceCheck"`
	// NextImageCheck verifies every public-relative asset reference resolves to
	// a real file under the app's public/ dir (next build does not). Static.
	NextImageCheck bool `json:"nextImageCheck"`
	// NextLinkCheck verifies internal links resolve to a real route (static
	// route analysis) and/or that a running site has no 4xx links (crawl);
	// mode is set in nextLinkCheck.mode.
	NextLinkCheck bool `json:"nextLinkCheck"`
}

// AppConfig represents configuration for a single app
type AppConfig struct {
	Path            string           `json:"path"`
	Filter          string           `json:"filter"`
	TestCommand     string           `json:"testCommand,omitempty"`
	TestArgs        []string         `json:"testArgs,omitempty"`        // Extra args passed to the test runner after "--" (e.g., ["--watchman=false"])
	NodeMemoryMB    int              `json:"nodeMemoryMB,omitempty"`    // Memory limit for Node.js (e.g., 8192 for 8GB)
	TypecheckFilter *TypecheckFilter `json:"typecheckFilter,omitempty"` // Per-app override for typecheck settings
	SkipLint        bool             `json:"skipLint,omitempty"`        // Skip lint for this app (typecheck still runs)
	SkipTypecheck   bool             `json:"skipTypecheck,omitempty"`   // Skip typecheck for this app (lint still runs)
}

// TypecheckFilter configures which TypeScript errors to filter out
type TypecheckFilter struct {
	ErrorCodes     []string `json:"errorCodes"`
	ExcludePaths   []string `json:"excludePaths"`
	ErrorCodePaths []string `json:"errorCodePaths"`
	SkipLibCheck   *bool    `json:"skipLibCheck,omitempty"` // If false, check .d.ts files (stricter). Default: true
	UseBuildMode   *bool    `json:"useBuildMode,omitempty"` // If true, use `tsc -b` instead of `tsc --noEmit`. Default: false
	// UseTsgo swaps the TypeScript binary from `tsc` (TS 6.x) to `tsgo`
	// (TypeScript 7 native preview, shipped by @typescript/native-preview).
	// All other flags pass through unchanged. Default: false (use tsc).
	UseTsgo *bool `json:"useTsgo,omitempty"`
}

// LintFilter configures which lint errors to filter out
type LintFilter struct {
	Rules          []string `json:"rules"`
	ExcludePaths   []string `json:"excludePaths"`
	Linter         string   `json:"linter"`         // "eslint" (default) or "oxlint"
	IgnoreWarnings bool     `json:"ignoreWarnings"` // If true, filter out warning-level lint errors
}

// LintStagedConfig configures lint-staged execution
type LintStagedConfig struct {
	PackageManager string            `json:"packageManager"` // "pnpm" (default), "bun", "npm", "yarn"
	Env            map[string]string `json:"env"`            // Environment variables to set (e.g., {"COREPACK_ENABLE_STRICT": "0"})
}

// GoLintConfig configures Go linting
type GoLintConfig struct {
	Paths []string `json:"paths"`
	Tool  string   `json:"tool"`
}

// GoMissingTestsCheckConfig configures the per-package Go missing-tests gate.
// A package directory with non-test .go source passes iff it contains at
// least one *_test.go file.
type GoMissingTestsCheckConfig struct {
	// Mode is "staged" (default) — only check directories with a staged .go
	// source file — or "all" — walk AppPaths and report every untested package.
	Mode string `json:"mode"`
	// AppPaths restricts scanning to files whose project-relative path contains
	// at least one of these substrings. Empty = whole project.
	AppPaths []string `json:"appPaths"`
	// ExcludePaths skips files whose project-relative path contains any of these
	// substrings. Exclusions always win over AppPaths.
	ExcludePaths []string `json:"excludePaths"`
}

// GoTestsConfig configures the `go test` runner.
type GoTestsConfig struct {
	// Modules lists project-relative directories to run `go test ./...` in.
	// Empty defaults to ".".
	Modules []string `json:"modules"`
	// AffectedOnly runs `go test .` only in the directories of staged Go files
	// instead of `go test ./...` across the whole module.
	AffectedOnly bool `json:"affectedOnly"`
}

// ConvexConfig configures Convex validation
type ConvexConfig struct {
	Path string `json:"path"`
	// Mode selects how the backend is validated: "codegen" (default) checks
	// that functions compile without touching the deployment, "dev" pushes to
	// the configured deployment. See ConvexModeCodegen / ConvexModeDev.
	Mode string `json:"mode"`
	// SuccessMarker is the line that means success. Only consulted in "dev"
	// mode; "codegen" has no completion line and is judged on exit status.
	SuccessMarker  string `json:"successMarker"`
	PackageManager string `json:"-"` // Inherited from global config
}

// BuildConfig configures build checks
type BuildConfig struct {
	Apps []string `json:"apps"`
}

// TestConfig configures test execution behavior
type TestConfig struct {
	// AffectedOnly runs tests only for apps with staged changes (unless shared paths changed)
	AffectedOnly bool `json:"affectedOnly"`
	// RunOnSharedChanges forces all tests when shared paths change (default: true)
	RunOnSharedChanges *bool `json:"runOnSharedChanges,omitempty"`
	// AppOverrides allows per-app test configuration
	AppOverrides map[string]AppTestOverride `json:"appOverrides,omitempty"`
	// Retries is the number of times a failing test command is re-executed
	// before the gate reports failure. Each retry runs the full app test
	// command again. Default: 0 (no retries — strict). Set to 1 to absorb
	// the most common environmental flake class (worker contention, fake
	// timer + scheduler interaction) without masking real regressions.
	Retries int `json:"retries,omitempty"`
	// FlakyTestFiles is a list of project-relative test file paths that
	// receive *one additional retry beyond Retries* before counting as a
	// failure. These are tests known to be environmentally flaky (e.g. a
	// scheduler test whose pump budget is occasionally starved by worker
	// contention). The retry only kicks in when one of these files appears
	// in the failure output; otherwise the standard Retries policy applies.
	// Listing a test here is a contract: it MUST pass on a clean isolated
	// run. The quarantine bypasses gate failures, not real bugs.
	FlakyTestFiles []string `json:"flakyTestFiles,omitempty"`
}

// AppTestOverride configures test behavior for a specific app
type AppTestOverride struct {
	// Enabled overrides the global tests feature flag (nil = use global, true = force on, false = force off)
	Enabled *bool `json:"enabled,omitempty"`
	// Skip disables tests for this app entirely (deprecated: use enabled: false instead)
	Skip bool `json:"skip"`
	// OnlyWhenAffected runs tests only when this app has staged changes (overrides global affectedOnly)
	OnlyWhenAffected *bool `json:"onlyWhenAffected,omitempty"`
}

// TestCoverageConfig configures test file coverage checking
type TestCoverageConfig struct {
	// AppPaths specifies which app paths to check (e.g., ["apps/admin", "apps/portal"])
	AppPaths []string `json:"appPaths"`
	// RequireTestFolders specifies which CRUD folders require test files
	// e.g., ["hooks", "read", "create", "update", "delete", "utils"]
	RequireTestFolders []string `json:"requireTestFolders"`
	// ExcludeFiles specifies file patterns to exclude (e.g., ["index.ts", "*.types.ts"])
	ExcludeFiles []string `json:"excludeFiles"`
	// ExcludePaths specifies path patterns to exclude entirely
	ExcludePaths []string `json:"excludePaths"`
}

// MaestroValidationConfig configures static validation that every id: selector
// used in a Maestro .yaml flow still resolves to a testID in source code.
// This catches drift when screens are refactored and testIDs rename/disappear
// without the corresponding flows being updated.
type MaestroValidationConfig struct {
	// FlowsDir is the repo-root-relative directory containing Maestro .yaml
	// flow files (e.g. "apps/mobile/.maestro/flows"). The directory is walked
	// recursively and every .yaml file is scanned for `id:` selectors.
	FlowsDir string `json:"flowsDir"`
	// SourceDirs lists repo-root-relative directories to scan for testID
	// definitions. Must include any directory where production code lives that
	// the flows interact with — typically the mobile app source and any shared
	// UI package that hosts components rendered by the app.
	// Example: ["apps/mobile/app", "apps/mobile/components", "packages/mobile-ui/src"]
	SourceDirs []string `json:"sourceDirs"`
	// ExcludeDirs adds directory basenames to skip while walking SourceDirs.
	// These are merged with a built-in set of critical skips (node_modules,
	// build output, coverage, __tests__, etc) that are always applied.
	ExcludeDirs []string `json:"excludeDirs"`
}

// ChangelogConfig configures changelog fragment checking
type ChangelogConfig struct {
	// Mode: "global" (single .changelog/ at root), "per-app" (each app has its own .changelog/,
	// falls back to global for shared changes), or "required" (each affected app must have its own changelog)
	Mode string `json:"mode"`
	// GlobalDir: directory for global changelog (default: ".changelog")
	GlobalDir string `json:"globalDir,omitempty"`
	// Apps: list of app names that have changelog support (optional, defaults to all apps)
	Apps []string `json:"apps,omitempty"`
}

// SRPConfig configures Single Responsibility Principle checking
type SRPConfig struct {
	// AppPaths specifies which app paths to check (e.g., ["apps/portal", "apps/mobile"])
	// If empty, all files are checked
	AppPaths []string `json:"appPaths"`
	// ExcludePaths specifies path patterns to exclude from checking
	ExcludePaths []string `json:"excludePaths"`
	// HideWarnings suppresses warning output, only showing errors
	HideWarnings bool `json:"hideWarnings"`
	// ScreenHooks specifies which React hooks are forbidden in screen/page files.
	// Accepts individual hook names: "useState", "useReducer", "useContext",
	// "useCallback", "useEffect", "useMemo", or "all" to flag all of them.
	// If empty/unset, defaults to ["useState", "useReducer", "useContext"] for
	// backwards compatibility.
	ScreenHooks []string `json:"screenHooks"`
	// EnabledRules specifies which SRP rules to run. If empty/unset, all 6
	// existing rules run (backwards compatible). The "testRequired" rule is
	// always opt-in — it only runs when explicitly listed here.
	EnabledRules []string `json:"enabledRules"`
	// WarnOnly specifies rules whose violations should be downgraded to warnings
	// instead of errors, making them non-blocking.
	WarnOnly []string `json:"warnOnly"`
	// ErrorPaths specifies file path prefixes whose SRP violations should
	// remain errors even when their rule is listed in WarnOnly. This lets one
	// app (e.g. apps/mobile/) enforce SRP strictly while other apps stay at
	// warning-level. Matching is by prefix (Contains) against the file path.
	ErrorPaths []string `json:"errorPaths"`
	// ErrorScopes specifies git-change scopes whose SRP violations should
	// remain errors even when their rule is listed in WarnOnly. Accepted
	// values: "new" (files added in this commit, git diff-filter=A) and
	// "changed" (any staged file, diff-filter=ACMR). This is the ratchet
	// mechanism: existing files stay at warning-level while newly authored
	// or modified code is held to the strict standard.
	ErrorScopes []string `json:"errorScopes"`
	// WarningOnlyPaths is the per-file/per-directory exemption list. Any
	// file whose path matches an entry (substring match — accepts both
	// directory patterns like "apps/web/legacy/" and exact files like
	// "apps/web/foo/bar.tsx") has all SRP violations forced to "warning"
	// severity, overriding ErrorPaths, ErrorScopes, and srpStrictOnStaged.
	// Unlike ExcludePaths, the file stays in scope — violations still show
	// up in the audit report, they just don't block the commit.
	WarningOnlyPaths []string `json:"warningOnlyPaths"`
	// TestRequired configures the testRequired rule (requires enabledRules to include "testRequired").
	// Accepts a single object (legacy) or an array of named profiles.
	TestRequired TestRequiredProfiles `json:"testRequired"`
}

// TestRequiredConfig configures the testRequired SRP rule
type TestRequiredConfig struct {
	// Name identifies this profile in violation messages (e.g., "unit", "e2e-mobile").
	// Auto-generated as "profile-0", "profile-1", etc. when unset.
	Name string `json:"name"`
	// Scope controls which files are checked: "new" (only newly added), "changed" (all staged), "all" (everything).
	// Default: "new"
	Scope string `json:"scope"`
	// Extensions specifies which file extensions to check. Default: [".tsx"]
	Extensions []string `json:"extensions"`
	// TestPatterns specifies test file suffixes to look for. Default: [".test.tsx", ".test.ts", ".spec.tsx", ".spec.ts"]
	TestPatterns []string `json:"testPatterns"`
	// TestDirs specifies repo-root-relative directories to search for test files (basename match).
	// Example: ["apps/native/.maestro/flows"] matches source basename against files in that directory.
	TestDirs []string `json:"testDirs"`
	// IncludePaths restricts checking to files within these paths (prefix match). If empty, all paths checked.
	IncludePaths []string `json:"includePaths"`
	// ExcludePaths skips files matching these patterns (substring match)
	ExcludePaths []string `json:"excludePaths"`
	// ExcludeFiles skips exact filenames (basename match, e.g., "index.tsx", "_layout.tsx")
	ExcludeFiles []string `json:"excludeFiles"`
	// Severity controls violation severity: "error" or "warning".
	// When set explicitly, per-profile Severity wins over the global warnOnly
	// list — letting one app error on missing tests while others only warn.
	// When omitted, defaults to "error" unless "testRequired" is in WarnOnly,
	// in which case it falls back to "warning".
	Severity string `json:"severity"`
}

// TestRequiredProfiles wraps []TestRequiredConfig with backwards-compatible JSON unmarshaling.
// Accepts a single object (legacy) or an array of named profiles.
type TestRequiredProfiles []TestRequiredConfig

func (t *TestRequiredProfiles) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '[' {
		var arr []TestRequiredConfig
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*t = arr
		return nil
	}
	var single TestRequiredConfig
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	*t = []TestRequiredConfig{single}
	return nil
}

// resolvedScreenHooks returns the set of hooks to check in screens,
// expanding "all" and falling back to the default set when unset.
func (c SRPConfig) resolvedScreenHooks() map[string]bool {
	defaults := []string{"useState", "useReducer", "useContext"}

	hooks := c.ScreenHooks
	if len(hooks) == 0 {
		hooks = defaults
	}

	result := make(map[string]bool)
	for _, h := range hooks {
		if h == "all" {
			for _, d := range []string{"useState", "useReducer", "useContext", "useCallback", "useEffect", "useMemo"} {
				result[d] = true
			}
		} else {
			result[h] = true
		}
	}
	return result
}

// existingRules lists the 6 original SRP rules (not including testRequired)
var existingRules = []string{
	"directConvexImports",
	"stateInScreens",
	"multipleExports",
	"fileSize",
	"typeExportsLocation",
	"mixedConcerns",
}

// isRuleEnabled returns true if the given rule should run.
// When EnabledRules is empty/nil, all 6 existing rules run and testRequired is off.
// When EnabledRules is set, only listed rules run.
func (c SRPConfig) isRuleEnabled(rule string) bool {
	if len(c.EnabledRules) == 0 {
		// Backwards compat: all existing rules on, testRequired off
		for _, r := range existingRules {
			if r == rule {
				return true
			}
		}
		return false
	}
	for _, r := range c.EnabledRules {
		if r == rule {
			return true
		}
	}
	return false
}

// isWarnOnly returns true if violations from this rule should be downgraded to warnings.
func (c SRPConfig) isWarnOnly(rule string) bool {
	for _, r := range c.WarnOnly {
		if r == rule {
			return true
		}
	}
	return false
}

// isErrorPath returns true if file is covered by ErrorPaths and SRP
// violations against it should remain errors regardless of WarnOnly.
func (c SRPConfig) isErrorPath(file string) bool {
	for _, p := range c.ErrorPaths {
		if p == "" {
			continue
		}
		if strings.Contains(file, p) {
			return true
		}
	}
	return false
}

// isWarningOnlyPath reports whether a file is exempt from error-severity
// escalation. Substring match against WarningOnlyPaths entries — supports
// directory patterns ("apps/web/legacy/") and exact files
// ("apps/web/foo/bar.tsx") with the same syntax.
func (c SRPConfig) isWarningOnlyPath(file string) bool {
	for _, p := range c.WarningOnlyPaths {
		if p == "" {
			continue
		}
		if strings.Contains(file, p) {
			return true
		}
	}
	return false
}

// hasErrorScope reports whether the given change-scope ("new" or "changed")
// is configured in ErrorScopes.
func (c SRPConfig) hasErrorScope(scope string) bool {
	for _, s := range c.ErrorScopes {
		if s == scope {
			return true
		}
	}
	return false
}

// withStagedStrict returns a copy of c with "changed" added to ErrorScopes
// when strict==true. Used to implement features.srpStrictOnStaged without
// mutating the loaded config. Idempotent: a no-op when "changed" is already
// present or strict is false.
func (c SRPConfig) withStagedStrict(strict bool) SRPConfig {
	if !strict || c.hasErrorScope("changed") {
		return c
	}
	scopes := make([]string, len(c.ErrorScopes), len(c.ErrorScopes)+1)
	copy(scopes, c.ErrorScopes)
	scopes = append(scopes, "changed")
	c.ErrorScopes = scopes
	return c
}

// resolvedTestRequired returns test profiles with defaults applied per profile.
func (c SRPConfig) resolvedTestRequired() []TestRequiredConfig {
	defaults := TestRequiredConfig{
		Scope:        "new",
		Extensions:   []string{".tsx"},
		TestPatterns: []string{".test.tsx", ".test.ts", ".spec.tsx", ".spec.ts"},
	}
	if len(c.TestRequired) == 0 {
		return []TestRequiredConfig{defaults}
	}
	result := make([]TestRequiredConfig, len(c.TestRequired))
	for i, cfg := range c.TestRequired {
		if cfg.Scope == "" {
			cfg.Scope = defaults.Scope
		}
		if len(cfg.Extensions) == 0 {
			cfg.Extensions = defaults.Extensions
		}
		if len(cfg.TestPatterns) == 0 {
			cfg.TestPatterns = defaults.TestPatterns
		}
		if cfg.Name == "" {
			cfg.Name = fmt.Sprintf("profile-%d", i)
		}
		result[i] = cfg
	}
	return result
}

// loadConfig loads configuration from .pre-commit.json (supports JSONC comments)
func loadConfig() (*Config, error) {
	configPath := ".pre-commit.json"

	data, err := jsonc.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	applyDefaults(&config)

	return &config, nil
}

// defaultConfig returns the default configuration when no config file exists
func defaultConfig() *Config {
	return &Config{
		Apps: map[string]AppConfig{},
		Features: Features{
			Lint:       true,
			Typecheck:  true,
			LintStaged: true,
		},
		GoLint: GoLintConfig{
			Tool: "golangci-lint",
		},
		Convex: ConvexConfig{
			SuccessMarker: "Convex functions ready!",
		},
	}
}

// GetTypecheckFilter returns the effective typecheck filter for an app,
// merging global settings with any per-app overrides
func GetTypecheckFilter(global TypecheckFilter, appOverride *TypecheckFilter) TypecheckFilter {
	if appOverride == nil {
		return global
	}

	result := global

	// Per-app settings override global when specified
	if len(appOverride.ErrorCodes) > 0 {
		result.ErrorCodes = appOverride.ErrorCodes
	}
	if len(appOverride.ExcludePaths) > 0 {
		result.ExcludePaths = appOverride.ExcludePaths
	}
	if len(appOverride.ErrorCodePaths) > 0 {
		result.ErrorCodePaths = appOverride.ErrorCodePaths
	}
	if appOverride.SkipLibCheck != nil {
		result.SkipLibCheck = appOverride.SkipLibCheck
	}
	if appOverride.UseBuildMode != nil {
		result.UseBuildMode = appOverride.UseBuildMode
	}
	if appOverride.UseTsgo != nil {
		result.UseTsgo = appOverride.UseTsgo
	}

	return result
}

// applyDefaults sets default values for fields that weren't specified in JSON
func applyDefaults(config *Config) {
	if config.PackageManager == "" {
		config.PackageManager = "pnpm"
	}
	if config.GoLint.Tool == "" {
		config.GoLint.Tool = "golangci-lint"
	}
	if config.Convex.SuccessMarker == "" {
		config.Convex.SuccessMarker = "Convex functions ready!"
	}
	if config.Convex.PackageManager == "" {
		config.Convex.PackageManager = config.PackageManager
	}
	// LintStagedConfig inherits from global if not set
	if config.LintStagedConfig.PackageManager == "" {
		config.LintStagedConfig.PackageManager = config.PackageManager
	}
	// Merge global env into LintStagedConfig.Env (LintStagedConfig takes precedence)
	if len(config.Env) > 0 && config.LintStagedConfig.Env == nil {
		config.LintStagedConfig.Env = make(map[string]string)
	}
	for k, v := range config.Env {
		if _, exists := config.LintStagedConfig.Env[k]; !exists {
			config.LintStagedConfig.Env[k] = v
		}
	}
	// Default: run all tests when shared paths change
	if config.TestConfig.RunOnSharedChanges == nil {
		defaultTrue := true
		config.TestConfig.RunOnSharedChanges = &defaultTrue
	}
	// Default: skip lib check for backwards compatibility (lenient mode)
	if config.TypecheckFilter.SkipLibCheck == nil {
		defaultTrue := true
		config.TypecheckFilter.SkipLibCheck = &defaultTrue
	}
	// Default: global changelog mode
	if config.ChangelogConfig.Mode == "" {
		config.ChangelogConfig.Mode = "global"
	}
	if config.ChangelogConfig.GlobalDir == "" {
		config.ChangelogConfig.GlobalDir = ".changelog"
	}
}
