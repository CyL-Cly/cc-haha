package agentcatalog

import (
	"strings"
	"testing"
)

func TestBlockScalarPreservesBlankAndCommentText(t *testing.T) {
	f, b, e := Frontmatter("---\nname: Reader\ndescription: |-\n  first\n\n  # literal comment\n  last\nmodel: inherit\n---\n  body  ")
	if e != nil || f.String("description") != "first\n\n# literal comment\nlast" || strings.TrimSpace(b) != "body" {
		t.Fatalf("description=%q body=%q error=%v", f.String("description"), b, e)
	}
}

func TestExistingFrontmatterFormsAndInvalidAttemptSelection(t *testing.T) {
	cases := []struct{ input, key, want, body string }{
		{"no frontmatter", "name", "", "no frontmatter"},
		{"---\n# empty\n---\nbody", "name", "", "body"},
		{"---\nname: 'it''s reader'\ndescription: \"quoted\\nline\"\n---\nbody", "name", "it's reader", "body"},
		{"---\ndescription: >-\n  first\n  second\n\n  next paragraph\n---\nbody", "description", "first second\nnext paragraph", "body"},
		{"---\ndescription: |+\n  first\n\n---\nbody", "description", "first\n\n", "body"},
		{"---\ndescription: |\n  first\n\n---\nbody", "description", "first\n", "body"},
		{"---\nname: Reader\ndescription: Use for: private work\n---\nbody", "description", "Use for: private work", "body"},
		{"---\nname: [bad\n---\nbody", "name", "[bad", "body"},
		{"---\nname: \"unterminated\n---\nbody", "name", "", "body"},
		{"---\n- one\n- two\n---\nbody", "name", "", "body"},
		{"---\nnull\n---\nbody", "name", "", "body"},
	}
	for _, c := range cases {
		f, b, e := Frontmatter(c.input)
		if e != nil || f.String(c.key) != c.want || b != c.body {
			t.Errorf("%q got=%q body=%q err=%v", c.input, f.String(c.key), b, e)
		}
	}
	f, _, e := Frontmatter("---\nname: Reader\ndescription: Test\nfuture:\n  list:\n    - alpha\n    - {nested: false, 'quoted key': [null, 1, yes]}\n  \"number\": +12\n---\nbody")
	if e != nil || string(f.Get("future")) != `{"list":["alpha",{"nested":false,"quoted key":[null,1,"yes"]}],"number":12}` {
		t.Fatalf("future=%s %v", f.Get("future"), e)
	}
}
func TestExpandedSourceYamlFormsAndInvalidSurrogateFallback(t *testing.T) {
	for _, c := range []struct{ value, expected string }{
		{"&anchor value", `"value"`}, {"*anchor", `"*anchor"`}, {"!tag value", `"value"`},
		{"%TAG directive", `"%TAG directive"`}, {".inf", "null"}, {"-.Inf", "null"},
		{"+.inf", "null"}, {".nan", "null"}, {"0x10", "16"}, {"0o10", "8"},
		{"1_000", `"1_000"`}, {`"\x41"`, `"A"`}, {"|2\n  body", `"body\n"`},
	} {
		fm, body, e := Frontmatter("---\nname: Reader\ndescription: " + c.value + "\n---\nbody")
		if e != nil || string(fm.Get("description")) != c.expected || body != "body" {
			t.Errorf("%q fm=%s body=%q error=%v", c.value, fm.Get("description"), body, e)
		}
	}
	invalid, body, e := Frontmatter("---\nname: Reader\ndescription: \"\\ud800\"\n---\nbody")
	if e != nil || len(invalid.OwnProperties()) != 0 || body != "body" {
		t.Fatalf("invalid YAML surrogate must use Source empty-frontmatter fallback: %+v %q %v", invalid, body, e)
	}
	f, _, e := Frontmatter("---\nname: \"\\ud83d\\ude00\"\ndescription: accepted pair\n---\nbody")
	if e != nil || len(f.OwnProperties()) != 0 {
		t.Fatalf("Source YAML rejects surrogate escape pairs: %v %+v", e, f)
	}
	f, _, e = Frontmatter("---\nname: 😀\ndescription: \"\\U0001F600\"\n---\nbody")
	if e != nil || f.String("name") != "😀" || f.String("description") != "😀" {
		t.Fatalf("valid Unicode text/code point lost: %v %+v", e, f)
	}
}
func TestAgentParserPreservesSourceValidityAndMemoryRules(t *testing.T) {
	good := []string{"model: '\uFEFF InHeRiT \uFEFF'", "model: custom.thirdparty", "effort: -13", "effort: '-0012'", "effort: [HIGH]", "effort: 2147483648", "effort: max", "memory: invalid", "memory: local"}
	for _, extra := range good {
		f, b, e := Frontmatter("---\nname: Reader\ndescription: 'line\\nline'\n" + extra + "\n---\n\uFEFF body \uFEFF")
		if e != nil {
			t.Fatal(e)
		}
		d, why, e := parse("private.md", "projectSettings", f, b)
		if e != nil || d == nil || why != "" || d.Description != "line\nline" || d.Body != "body" {
			t.Errorf("%s %+v %q %v", extra, d, why, e)
		}
	}
	bad := []struct{ input, why string }{{"name: false\ndescription: test", `Missing required "name"`}, {"name: Reader\ndescription: null", `Missing required "description"`}, {"name: Reader\ndescription: test\nmodel: null", `Invalid "model"`}, {"name: Reader\ndescription: test\nmodel: ''", `Invalid "model"`}, {"name: Reader\ndescription: test\neffort: 2.5", `Invalid "effort"`}, {"name: Reader\ndescription: test\neffort: false", `Invalid "effort"`}, {"name: Reader\ndescription: test\neffort: {}", `Invalid "effort"`}, {"name: Reader\ndescription: test\neffort: '1e2'", `Invalid "effort"`}}
	for _, c := range bad {
		f, b, e := Frontmatter("---\n" + c.input + "\n---\nb")
		if e != nil {
			t.Fatal(e)
		}
		d, why, e := parse("x", "userSettings", f, b)
		if d != nil || e != nil || !strings.Contains(why, c.why) {
			t.Errorf("%s d=%+v why=%q error=%v", c.input, d, why, e)
		}
	}
}

func TestInvalidAttemptErrorSelectsEffortBeforeModel(t *testing.T) {
	f, b, e := Frontmatter("---\nname: Reader\ndescription: private\nmodel: null\neffort: false\n---\nbody")
	if e != nil {
		t.Fatal(e)
	}
	d, why, e := parse("private.md", "userSettings", f, b)
	if d != nil || e != nil || !strings.Contains(why, `Invalid "effort"`) {
		t.Fatalf("definition=%+v why=%q error=%v", d, why, e)
	}
}

func TestUnknownJSONFlowValuesAndExpandedYamlRemain(t *testing.T) {
	f, _, e := Frontmatter("---\nname: Reader\ndescription: private\nfuture: {\"key\":true,\"nested\":[1,2]}\n---\nbody")
	if e != nil || f.String("name") != "Reader" || string(f.Get("future")) != `{"key":true,"nested":[1,2]}` {
		t.Fatalf("valid unknown fields lost=%s name=%q error=%v", f.Get("future"), f.String("name"), e)
	}
	for _, c := range []struct{ text, key, expected string }{
		{"description: first\n  continuation", "description", `"first continuation"`},
		{"future:\n  - key: value\n    other: true", "future", `[{"key":"value","other":true}]`},
	} {
		fm, body, e := Frontmatter("---\nname: Reader\n" + c.text + "\n---\nbody")
		if e != nil || fm.String("name") != "Reader" || string(fm.Get(c.key)) != c.expected || body != "body" {
			t.Errorf("valid YAML lost: %q value=%s body=%q error=%v", c.text, fm.Get(c.key), body, e)
		}
	}
}
