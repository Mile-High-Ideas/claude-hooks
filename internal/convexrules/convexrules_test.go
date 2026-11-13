package convexrules

import "testing"

func TestRuleKey(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"convex(type-exports-location)", "type-exports-location"},
		{"convex(file-size)", "file-size"},
		{"@convex-dev(explicit-table-ids)", "@convex-dev/explicit-table-ids"},
		{"@convex-dev(require-args-validator)", "@convex-dev/require-args-validator"},
		{"eslint(no-unused-vars)", ""},
		{"typescript(no-explicit-any)", ""},
		{"convexdev(explicit-table-ids)", ""},
		{"convex()", ""},
		{"convex(", ""},
		{"(file-size)", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := RuleKey(tt.code); got != tt.want {
			t.Errorf("RuleKey(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestCleanMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "drops typed-linting advice",
			msg:  "Database get call should include an explicit table name.\n\nNote: type-aware linting is not enabled in your project. We recommend enabling it.",
			want: "Database get call should include an explicit table name.",
		},
		{
			name: "leaves other messages alone",
			msg:  "Move exported types to a .types.ts file",
			want: "Move exported types to a .types.ts file",
		},
		{
			name: "keeps an inline Note that is not the advice",
			msg:  "Too many functions. Note: split by domain.",
			want: "Too many functions. Note: split by domain.",
		},
	}
	for _, tt := range tests {
		if got := CleanMessage(tt.msg); got != tt.want {
			t.Errorf("%s: CleanMessage() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
