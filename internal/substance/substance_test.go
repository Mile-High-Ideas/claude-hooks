package substance

import (
	"strings"
	"testing"
)

func TestCountCodeLines(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{name: "empty", content: ``, want: 0},
		{name: "blank only", content: "\n\n  \n", want: 0},
		{
			name:    "code with blanks",
			content: "import x\n\nfunction y() {\n  return x\n}\n",
			want:    4,
		},
		{
			name: "line comments excluded",
			content: `// header
import x // trailing comment
const y = 1
// footer
`,
			want: 2,
		},
		{
			name: "single-line block comment excluded",
			content: `/* header */
const x = 1
`,
			want: 1,
		},
		{
			name: "multi-line block comment excluded",
			content: `/*
 * doc
 */
const x = 1
`,
			want: 1,
		},
		{
			name: "block comment with code after */ counts that line",
			content: `/* */ const x = 1
`,
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CountCodeLines(tc.content); got != tc.want {
				t.Errorf("CountCodeLines() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestIsUIComponent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "react default import",
			content: `import React from "react"`,
			want:    true,
		},
		{
			name:    "react named import",
			content: `import { useState } from "react"`,
			want:    true,
		},
		{
			name:    "JSX component",
			content: `export const X = () => <Card />`,
			want:    true,
		},
		{
			name:    "JSX fragment",
			content: `export const X = () => <>{children}</>`,
			want:    true,
		},
		{
			name:    "namespaced component",
			content: `export const X = () => <Card.Body>x</Card.Body>`,
			want:    true,
		},
		{
			name:    "lowercase HTML-only — no component",
			content: `export const X = () => "<div>x</div>"`,
			want:    false,
		},
		{
			name:    "pure utility",
			content: `export function add(a: number, b: number) { return a + b }`,
			want:    false,
		},
		{
			name:    "import from elsewhere",
			content: `import { thing } from "lodash"`,
			want:    false,
		},
		{
			name:    "generic function declaration",
			content: `export function f<AssignmentId extends string>(x: AssignmentId[]): AssignmentId[] { return x; }`,
			want:    false,
		},
		{
			name:    "Promise return type",
			content: `export async function load(id: string): Promise<Foo> { return fetchFoo(id) }`,
			want:    false,
		},
		{
			name:    "Record type",
			content: `export function index<K extends string, V>(rows: V[], key: (v: V) => K): Record<K, V> { return {} as Record<K, V> }`,
			want:    false,
		},
		{
			name:    "Array type",
			content: `export const uniq = <T>(xs: Array<T>): Array<T> => Array.from(new Set(xs))`,
			want:    false,
		},
		{
			name:    "nested generics and arrows in a utility module",
			content: "type Fn = () => void\nexport const m: Map<string, Array<Promise<Result>>> = new Map()\nexport const run = (f: Fn) => f()",
			want:    false,
		},
		{
			name:    "View with children, no react import",
			content: "export const X = () => (\n  <View>\n    {children}\n  </View>\n)",
			want:    true,
		},
		{
			name:    "component with props and arrow attribute",
			content: `export const X = () => <Button title="Go" onPress={() => go()} />`,
			want:    true,
		},
		{
			name:    "self-closing without space",
			content: `export const X = () => <Spinner/>`,
			want:    true,
		},
		{
			name:    "fragment wrapping a component",
			content: "export const X = () => (\n  <>\n    <Header title={t} />\n  </>\n)",
			want:    true,
		},
		{
			name:    "generic component props with JSX body",
			content: "export function List<T>(props: ListProps<T>) {\n  return <FlatList data={props.items} />\n}",
			want:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUIComponent(tc.content); got != tc.want {
				t.Errorf("IsUIComponent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasInteraction(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "fireEvent.click", content: `fireEvent.click(button)`, want: true},
		{name: "fireEvent.change", content: `fireEvent.change(input, { target })`, want: true},
		{name: "userEvent.click v13", content: `userEvent.click(button)`, want: true},
		{name: "userEvent.type v13", content: `userEvent.type(input, "x")`, want: true},
		{name: "user.click v14", content: `await user.click(button)`, want: true},
		{name: "user.type v14", content: `await user.type(input, "hello")`, want: true},
		{name: "act async", content: `await act(async () => {})`, want: true},
		{name: "act sync", content: `act(() => {})`, want: true},
		{
			name:    "render-only test — no interaction",
			content: `render(<X />); expect(getByText("hi")).toBe("hi")`,
			want:    false,
		},
		{
			name:    "screen.getByText is not interaction",
			content: `expect(screen.getByText("Save")).toBeOnTheScreen()`,
			want:    false,
		},
		{name: "empty", content: ``, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasInteraction(tc.content); got != tc.want {
				t.Errorf("HasInteraction() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCountBranches(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{name: "no branches", content: `const x = 1`, want: 0},
		{
			name: "if + else if + else",
			content: `
if (a) { x }
else if (b) { y }
else { z }
`,
			want: 2, // 'if' and 'else if' both contain 'if (', else has no '(' so 2
		},
		{
			name: "switch case",
			content: `
switch (x) {
  case 1: break
  case 2: break
  default: break
}
`,
			want: 2,
		},
		{
			name:    "optional chaining is not a branch",
			content: `const x = obj?.foo?.bar`,
			want:    0,
		},
		{
			name:    "ternary is not counted (intentionally)",
			content: `const x = a ? b : c`,
			want:    0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CountBranches(tc.content); got != tc.want {
				t.Errorf("CountBranches() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCountItBlocks(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{name: "single it()", content: `it("x", () => {})`, want: 1},
		{name: "single test()", content: `test("x", () => {})`, want: 1},
		{
			name: "it + test mixed",
			content: `
it("a", () => {})
test("b", () => {})
it("c", () => {})
`,
			want: 3,
		},
		{name: "it.skip", content: `it.skip("x", () => {})`, want: 1},
		{name: "it.only", content: `it.only("x", () => {})`, want: 1},
		{name: "test.each", content: `test.each([1,2])("x", () => {})`, want: 1},
		{
			name:    "describe is not counted",
			content: `describe("group", () => { it("x", () => {}) })`,
			want:    1,
		},
		{name: "nothing", content: ``, want: 0},
		{
			name:    "the word 'commit' or 'submit' should not match",
			content: `function commit() {}; const submit = ()=>{}`,
			want:    0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CountItBlocks(tc.content); got != tc.want {
				t.Errorf("CountItBlocks() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCheck_LOCRatio(t *testing.T) {
	// 100-line source, 10-line test = 10% < 30% threshold.
	source := strings.Repeat("const x = 1\n", 100)
	test := `import { describe, it, expect } from "vitest"
import { Foo } from "./Foo"
describe("Foo", () => {
  it("renders", () => {
    expect(Foo).toBe(Foo)
  })
})
`
	cfg := DefaultConfig
	violations := Check(source, test, cfg)
	if len(violations) == 0 {
		t.Fatal("expected loc_ratio_below violation, got none")
	}
	found := false
	for _, v := range violations {
		if v.Kind == "loc_ratio_below" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected loc_ratio_below kind, got %+v", violations)
	}
}

func TestCheck_LOCRatio_SmallSourceSkipped(t *testing.T) {
	// Source under MinSourceLOCForCheck (50). LOC gate should not fire.
	source := strings.Repeat("const x = 1\n", 30)
	test := `it("x", () => { expect(x).toBe(1) })`
	cfg := DefaultConfig
	violations := Check(source, test, cfg)
	for _, v := range violations {
		if v.Kind == "loc_ratio_below" {
			t.Errorf("did not expect loc_ratio_below for small source; got %v", v)
		}
	}
}

func TestCheck_NoInteraction(t *testing.T) {
	source := `import React from "react"
export const Card = ({ onClick, children }: { onClick: () => void; children: React.ReactNode }) => (
  <div onClick={onClick}>{children}</div>
)
`
	test := `import { render, screen } from "@testing-library/react"
import { Card } from "./Card"
describe("Card", () => {
  it("renders", () => {
    render(<Card onClick={() => {}}>hi</Card>)
    expect(screen.getByText("hi")).toBeOnTheScreen()
  })
})
`
	cfg := DefaultConfig
	cfg.MinTestSourceRatio = 0       // disable LOC gate for this test
	cfg.MinSourceLOCForCheck = 0     // ensure not skipped
	cfg.BranchToItRatio = 0          // disable branch gate
	violations := Check(source, test, cfg)
	if len(violations) != 1 || violations[0].Kind != "no_interaction_in_ui_test" {
		t.Errorf("expected single no_interaction_in_ui_test violation, got %+v", violations)
	}
}

func TestCheck_InteractionPresent(t *testing.T) {
	source := `import React from "react"
export const Card = () => <div onClick={() => {}}>x</div>
`
	test := `import { render, fireEvent, screen } from "@testing-library/react"
fireEvent.click(screen.getByRole("button"))
`
	cfg := Config{RequireInteraction: true}
	violations := Check(source, test, cfg)
	for _, v := range violations {
		if v.Kind == "no_interaction_in_ui_test" {
			t.Errorf("did not expect no_interaction_in_ui_test, got %v", v)
		}
	}
}

func TestCheck_BranchImbalance(t *testing.T) {
	// Source has 8 if() branches; test has 1 it(). With ratio=4, we need
	// ceil(8/4)=2 it blocks. We have 1, so violation fires.
	var sb strings.Builder
	sb.WriteString("export function f(x: any) {\n")
	for i := 0; i < 8; i++ {
		sb.WriteString("  if (x.a) { return 1 }\n")
	}
	sb.WriteString("  return 0\n}\n")
	// Pad to exceed MinSourceLOCForCheck floor.
	sb.WriteString(strings.Repeat("// pad\n", 60))
	source := sb.String()

	test := `import { f } from "./f"
it("returns 0 when nothing matches", () => {
  expect(f({})).toBe(0)
})
`
	cfg := DefaultConfig
	cfg.MinTestSourceRatio = 0     // isolate gate
	cfg.RequireInteraction = false // isolate gate
	cfg.MinSourceLOCForCheck = 0   // bypass floor for this test
	violations := Check(source, test, cfg)
	found := false
	for _, v := range violations {
		if v.Kind == "branch_imbalance" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected branch_imbalance violation, got %+v", violations)
	}
}

func TestCheck_AllPass(t *testing.T) {
	// 100-line source with one branch; substantial test that interacts.
	source := `import React, { useState } from "react"
` + strings.Repeat("// padding\n", 50) + `
export const Counter = () => {
  const [n, setN] = useState(0)
  if (n < 0) {
    return <div>negative</div>
  }
  return <button onClick={() => setN(n + 1)}>{n}</button>
}
` + strings.Repeat("// more padding\n", 50)

	test := `import { render, fireEvent, screen } from "@testing-library/react"
import { Counter } from "./Counter"

describe("Counter", () => {
  it("starts at 0", () => {
    render(<Counter />)
    expect(screen.getByRole("button").textContent).toBe("0")
  })

  it("increments on click", () => {
    render(<Counter />)
    fireEvent.click(screen.getByRole("button"))
    expect(screen.getByRole("button").textContent).toBe("1")
  })

  it("renders many clicks", () => {
    render(<Counter />)
    for (let i = 0; i < 5; i++) {
      fireEvent.click(screen.getByRole("button"))
    }
    expect(screen.getByRole("button").textContent).toBe("5")
  })
})
` + strings.Repeat("// matter\n", 30)

	violations := Check(source, test, DefaultConfig)
	if len(violations) != 0 {
		t.Errorf("expected no violations, got %+v", violations)
	}
}

func TestCheck_SourceTooSmall(t *testing.T) {
	// Source under floor → all gates skip.
	source := `export const id = (x: number) => x`
	test := `it("returns input", () => { expect(id(1)).toBe(1) })`
	violations := Check(source, test, DefaultConfig)
	if len(violations) != 0 {
		t.Errorf("expected no violations for tiny source, got %+v", violations)
	}
}
