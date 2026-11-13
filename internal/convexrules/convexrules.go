// Package convexrules maps oxlint diagnostic codes to the rule ids that
// .convex-lint.json's errorRules list uses. The validate-convex edit hook and
// the pre-commit convexCheck gate share it, so both enforce the same set.
//
// Two oxlint JS plugins supply Convex rules:
//
//   - @milehighideas/oxlint-plugin-convex (meta.name "convex") reports codes
//     like "convex(type-exports-location)". errorRules lists them bare:
//     "type-exports-location".
//   - @convex-dev/eslint-plugin, loaded through oxlint's jsPlugins, reports
//     codes like "@convex-dev(explicit-table-ids)". errorRules lists them with
//     the namespace: "@convex-dev/explicit-table-ids".
package convexrules

import "strings"

// ConvexDevPrefix is the errorRules namespace for @convex-dev/eslint-plugin
// rules.
const ConvexDevPrefix = "@convex-dev/"

// LegacyEslintKeyWarning explains what to do with the eslintErrorRules key,
// which no longer has any effect.
const LegacyEslintKeyWarning = `.convex-lint.json "eslintErrorRules" is no longer read: ` +
	`load @convex-dev/eslint-plugin in .oxlintrc.json jsPlugins and list its rules ` +
	`in "errorRules" as "@convex-dev/<rule>" (e.g. "@convex-dev/explicit-table-ids")`

// RuleKey turns an oxlint diagnostic code into its errorRules id:
// "convex(file-size)" → "file-size", "@convex-dev(explicit-table-ids)" →
// "@convex-dev/explicit-table-ids". It returns "" for any other code.
func RuleKey(code string) string {
	plugin, rule, ok := splitCode(code)
	if !ok {
		return ""
	}
	switch plugin {
	case "convex":
		return rule
	case "@convex-dev":
		return ConvexDevPrefix + rule
	}
	return ""
}

// typedLintingNote starts the paragraph @convex-dev/eslint-plugin appends when
// it runs without a TypeScript program, telling the reader to enable
// typescript-eslint typed linting. Under oxlint that advice is wrong.
const typedLintingNote = "\n\nNote: type-aware linting is not enabled"

// CleanMessage drops the typed-linting advice from a diagnostic message.
func CleanMessage(msg string) string {
	if i := strings.Index(msg, typedLintingNote); i >= 0 {
		return strings.TrimSpace(msg[:i])
	}
	return msg
}

// splitCode splits "plugin(rule)" into its parts.
func splitCode(code string) (plugin, rule string, ok bool) {
	open := strings.IndexByte(code, '(')
	if open <= 0 || !strings.HasSuffix(code, ")") {
		return "", "", false
	}
	rule = code[open+1 : len(code)-1]
	if rule == "" {
		return "", "", false
	}
	return code[:open], rule, true
}
