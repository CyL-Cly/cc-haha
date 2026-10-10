package markdownfrontmatter

import (
	"encoding/json"
	"testing"
)

func TestHooksAliasesAndMultilineDescription(t *testing.T) {
	result, err := Parse("---\ndescription: Run the checks\n  before finishing\nshared: &shell {type: command, command: 'echo offline'}\nhooks:\n  PostToolUse:\n    - matcher: Write\n      hooks: [*shell]\n---\nDo the work.\n")
	if err != nil || result.Frontmatter.String("description") != "Run the checks before finishing" || result.Content != "Do the work.\n" {
		t.Fatalf("description/body lost: %+v %v", result, err)
	}
	if string(result.Frontmatter.Get("hooks")) != `{"PostToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"echo offline"}]}]}` {
		t.Fatalf("Hooks lost: %s", result.Frontmatter.Get("hooks"))
	}
}
func TestVersionCoercionSurvivesNonfiniteJSONProjection(t *testing.T) {
	result, err := Parse("---\nversion: .inf\nzero: -0\nnan: .nan\nfuture: [1, null, true]\n---\nbody")
	if err != nil || string(result.Frontmatter.Get("version")) != "null" || result.Get("version").String() != "Infinity" || !result.Get("version").Truthy() {
		t.Fatalf("version lost: %+v %v", result, err)
	}
	if result.Get("zero").Truthy() || result.Get("zero").String() != "0" || result.Get("nan").Truthy() || result.Get("nan").String() != "NaN" || result.Get("future").String() != "1,,true" {
		t.Fatal("JavaScript metadata coercion differs")
	}
}
func TestMalformedYamlFallbackAndDelimiterBody(t *testing.T) {
	result, err := Parse("---\ndescription: use this: before writing\nunknown: @literal\n---\n \t\nbody")
	if err != nil || result.Frontmatter.String("description") != "use this: before writing" || result.Frontmatter.String("unknown") != "@literal" || result.Content != "body" {
		t.Fatalf("fallback differs: %+v %v", result, err)
	}
	invalid, err := Parse("---\ndescription: \"unterminated\n---\nbody")
	if err != nil || len(invalid.Frontmatter.OwnProperties()) != 0 || invalid.Content != "body" {
		t.Fatalf("invalid fallback differs: %+v %v", invalid, err)
	}
}
func TestFirstClaimPositionAndNumericOwnKeyOrder(t *testing.T) {
	result, err := Parse("---\n'2': two\nx: first\n'1': one\nx: second\n---\nbody")
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := json.Marshal(result.Frontmatter)
	if err != nil || string(bytes) != `{"1":"one","2":"two","x":"second"}` || len(result.Properties) != 3 || result.Properties[0].Key != "1" || result.Properties[2].String != "second" {
		t.Fatalf("own-key order/value differs: %s %+v %v", bytes, result.Properties, err)
	}
}
