// Package substance detects test files that pass surface-level checks —
// the file exists in the right location and contains no all-weak-matcher
// stubs — but don't actually exercise the source they claim to cover.
//
// Three independent gates, all opt-in via Config:
//
//  1. LOC ratio: the test file's non-blank, non-comment line count must
//     be at least MinTestSourceRatio of the source file's. A 600-line
//     component paired with a 12-line render-and-check test is the
//     canonical "minimal test" pattern — it satisfies missingTestsCheck
//     and any individual lint rule, but covers no behavior.
//
//  2. UI interaction: when the source file appears to be a React
//     component (imports React, exports JSX, or contains JSX literals),
//     the test must call at least one of fireEvent.*, userEvent.*,
//     `await user.*`, or `act(`. A pure render-and-assert-on-DOM test
//     verifies the component mounts but never exercises any onClick,
//     onChange, onSubmit, etc. that's typically the entire point of a
//     UI component. RequireInteraction toggles this gate.
//
//  3. Branch-proportional it() count: the source file's branching
//     constructs (if, else if, switch case) imply N code paths; the
//     test file must have at least ceil(N / BranchToItRatio) it()/test()
//     blocks. A single `it("renders", …)` on a component with 14
//     branches is a clear sign the test isn't covering the conditional
//     paths.
//
// The checks are deliberately heuristic rather than coverage-exact. They
// run in milliseconds without executing the test runner — fast enough
// for a pre-commit gate, accurate enough that a deliberately written
// real test will pass and a typical "minimal" test won't.
package substance

import (
	"fmt"
	"regexp"
	"strings"
)

// Violation is a single substance failure for a source/test pair. Kind
// is a stable identifier callers can switch on; Message is a one-line
// human-readable description.
type Violation struct {
	Kind    string
	Message string
}

// Config controls which gates run and at what thresholds. Zero-value
// disables each gate (so callers always start from DefaultConfig and
// override only the keys they care about).
type Config struct {
	// MinTestSourceRatio is the minimum (testLOC / sourceLOC) ratio.
	// 0 disables the LOC-ratio gate. Sensible default: 0.3 (test
	// must be at least 30% of source LOC).
	MinTestSourceRatio float64

	// BranchToItRatio is the upper bound on (sourceBranches / testItBlocks).
	// 0 disables the branch-proportional gate. Sensible default: 4
	// (one it() block per 4 source branches; a 14-branch source needs
	// at least 4 it() blocks).
	BranchToItRatio int

	// RequireInteraction enables the UI-interaction gate when the source
	// looks like a React component.
	RequireInteraction bool

	// MinSourceLOCForCheck is the minimum source LOC below which the
	// LOC-ratio and branch gates are skipped. Tiny source files with a
	// few real assertions don't need ratio enforcement; the floor
	// prevents noise on 10-line utility files. Default: 50.
	MinSourceLOCForCheck int
}

// DefaultConfig is a reasonable starting point: 30% LOC, one it per
// 4 branches, interaction required for UI, ignore source files under
// 50 LOC.
var DefaultConfig = Config{
	MinTestSourceRatio:   0.3,
	BranchToItRatio:      4,
	RequireInteraction:   true,
	MinSourceLOCForCheck: 50,
}

// Check runs the configured gates against a source/test content pair and
// returns the violations found. Returns nil (no violations) when every
// enabled gate passes; never returns an error — heuristic checks fail
// open by design.
func Check(sourceContent, testContent string, cfg Config) []Violation {
	var violations []Violation

	sourceLOC := CountCodeLines(sourceContent)
	testLOC := CountCodeLines(testContent)

	// LOC ratio gate. Skip when source is very small (configurable floor)
	// to avoid demanding a 30-line test for a 12-line utility.
	if cfg.MinTestSourceRatio > 0 && sourceLOC >= cfg.MinSourceLOCForCheck {
		ratio := float64(testLOC) / float64(sourceLOC)
		if ratio < cfg.MinTestSourceRatio {
			violations = append(violations, Violation{
				Kind: "loc_ratio_below",
				Message: fmt.Sprintf(
					"test file is %d lines (%.0f%% of %d source lines); minimum is %.0f%% — write tests proportional to the surface they cover",
					testLOC, ratio*100, sourceLOC, cfg.MinTestSourceRatio*100,
				),
			})
		}
	}

	// UI interaction gate. Only fires when source looks like a UI
	// component AND the test contains zero interactions. Renders that
	// don't fire any event aren't testing the component's reason to
	// exist.
	if cfg.RequireInteraction && IsUIComponent(sourceContent) && !HasInteraction(testContent) {
		violations = append(violations, Violation{
			Kind: "no_interaction_in_ui_test",
			Message: "source is a UI component but test contains no fireEvent.*, userEvent.*, user.*, or act() calls — render-only tests don't exercise behavior",
		})
	}

	// Branch-proportional it() gate. Skip on small source files (same
	// floor as LOC-ratio) and skip when source has fewer branches than
	// the ratio threshold (a 2-branch file doesn't need 2 it() blocks).
	if cfg.BranchToItRatio > 0 && sourceLOC >= cfg.MinSourceLOCForCheck {
		branches := CountBranches(sourceContent)
		itBlocks := CountItBlocks(testContent)
		if branches >= cfg.BranchToItRatio {
			minIt := (branches + cfg.BranchToItRatio - 1) / cfg.BranchToItRatio
			if itBlocks < minIt {
				violations = append(violations, Violation{
					Kind: "branch_imbalance",
					Message: fmt.Sprintf(
						"source has %d branches (if/case); test has %d it()/test() blocks (need at least %d, one per %d branches)",
						branches, itBlocks, minIt, cfg.BranchToItRatio,
					),
				})
			}
		}
	}

	return violations
}

// CountCodeLines returns the number of non-blank, non-comment lines in
// content. Block comments are tracked with a simple state machine. Not
// perfect (won't recognize /* */ inside string literals) but accurate
// enough for ratio gates — the LOC counts on both sides drift in the
// same direction for any plausible drift case.
func CountCodeLines(content string) int {
	if content == "" {
		return 0
	}
	count := 0
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if inBlock {
			// Look for end of block on this line. If found, the rest of
			// the line *could* be code; we conservatively count it as
			// code only if there's content after the */.
			if idx := strings.Index(trimmed, "*/"); idx != -1 {
				inBlock = false
				rest := strings.TrimSpace(trimmed[idx+2:])
				if rest != "" && !strings.HasPrefix(rest, "//") {
					count++
				}
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			// Single-line block comment? Check for */ on same line.
			endIdx := strings.Index(trimmed[2:], "*/")
			if endIdx == -1 {
				inBlock = true
				continue
			}
			// Comment ends on this line; count the line as code only if
			// there's non-whitespace, non-comment content after the */.
			rest := strings.TrimSpace(trimmed[2+endIdx+2:])
			if rest != "" && !strings.HasPrefix(rest, "//") {
				count++
			}
			continue
		}
		count++
	}
	return count
}

// reactImportRe matches a top-of-file React import. Doesn't catch
// type-only imports (`import type { X } from "react"`) deliberately —
// type-only React import without JSX usage means the file is exporting
// types, not rendering, and shouldn't be subjected to UI gates.
var reactImportRe = regexp.MustCompile(`(?m)^[ \t]*import\s+[^;]*\bfrom\s+['"]react['"]`)

// jsxSelfClosingRe matches a self-closing component tag: `<Foo />`,
// `<Foo.Bar />`, `<Foo prop={x} onPress={() => go()} />`. The name must
// start with a capital letter (component) so HTML-like `<div/>` inside
// template strings doesn't count. Attributes may not contain `<`, which
// keeps a match from spanning across unrelated generic brackets.
var jsxSelfClosingRe = regexp.MustCompile(`<[A-Z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*(?:\s[^<]*?)?/>`)

// jsxClosingTagRe matches a component closing tag: `</Foo>`, `</Foo.Bar>`.
//
// Requiring a closed tag (self-closing, or a matching closer) is what
// separates JSX from TypeScript generics. An opening `<Foo` alone is
// ambiguous — `Promise<Foo>`, `Array<T>`, `Record<K, V>`, and
// `function f<T extends string>()` all look like one — but no TypeScript
// type syntax produces `</Foo>` or `<Foo ... />`.
var jsxClosingTagRe = regexp.MustCompile(`</[A-Z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*\s*>`)

// jsxFragmentRe matches the bare-fragment shape `<>...</>` which marks
// a file as JSX even when no component is rendered.
var jsxFragmentRe = regexp.MustCompile(`<>\s*[^<]*</>|<>\s*<`)

// IsUIComponent reports whether content looks like a React component
// source file. True when the file imports from "react" (any import shape)
// or contains a closed JSX component tag or fragment. TypeScript generics
// (`Promise<Foo>`, `f<T extends string>`) are not JSX. Pure data utilities
// and hooks files that don't render anything return false.
func IsUIComponent(content string) bool {
	if reactImportRe.MatchString(content) {
		return true
	}
	if jsxSelfClosingRe.MatchString(content) || jsxClosingTagRe.MatchString(content) {
		return true
	}
	if jsxFragmentRe.MatchString(content) {
		return true
	}
	return false
}

// interactionRe matches the common test-time interaction APIs:
//
//   - fireEvent.click(...), fireEvent.change(...), etc. (React Testing Library)
//   - userEvent.click(...), userEvent.type(...) (UE v13)
//   - user.click(...), user.type(...) (UE v14, after const user = userEvent.setup())
//   - await user.X(...) (the typical UE v14 call site)
//
// Doesn't match standalone `act(` because that's caused by other things
// too; act() is matched by actCallRe.
var interactionRe = regexp.MustCompile(`\b(fireEvent|userEvent)\s*\.\s*[a-zA-Z]+\s*\(|\buser\s*\.\s*[a-zA-Z]+\s*\(`)

// actCallRe matches `act(...)` invocations — async/sync. Used to detect
// behavior tests that flush effects without going through fireEvent.
var actCallRe = regexp.MustCompile(`\bact\s*\(`)

// HasInteraction reports whether content calls any of the recognized
// interaction APIs. Used by the UI-interaction gate.
func HasInteraction(content string) bool {
	if interactionRe.MatchString(content) {
		return true
	}
	if actCallRe.MatchString(content) {
		return true
	}
	return false
}

// branchRe matches `if (...)`, `else if (...)`, and `case X:` — the
// branching constructs that form distinct code paths. Deliberately
// excludes short-circuit (`&&` / `||`) and ternary (`?:`) because both
// produce too many false positives on TS code (optional chaining `?.`,
// non-null assertions `!`, conditional types). The gate is heuristic
// anyway — undercounting branches is preferable to overcounting on
// files that aren't really branchy.
var branchRe = regexp.MustCompile(`\bif\s*\(|\bcase\s+`)

// CountBranches returns the number of distinct branch constructs in
// content. Used by the branch-proportional it() gate.
func CountBranches(content string) int {
	return len(branchRe.FindAllString(content, -1))
}

// itBlockRe matches `it(...)`, `test(...)`, `it.skip(...)`, `test.only(...)`,
// `it.each(...)`. The chained form (.skip / .only / .each) counts as one
// it() block — same as a regular call.
var itBlockRe = regexp.MustCompile(`\b(it|test)(\s*\.\s*[a-zA-Z]+)?\s*\(`)

// CountItBlocks returns the number of test definition blocks in content.
// Used by the branch-proportional gate.
func CountItBlocks(content string) int {
	return len(itBlockRe.FindAllString(content, -1))
}
