package markdownfrontmatter

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func sourceCases() []string {
	out := []string{"body", "---\n---\nbody", "preface\n---\nname: item\n---\nbody", " ---\nname: item\n---\nbody"}
	values := []string{
		"name: Reader\ndescription: body", "description: first\n  continuation",
		"hooks:\n  - key: value\n    other: true", "defaults: &base { x: 1, y: true }\ncopy: *base",
		"future: &base [one, two]\ncopy: *base", "base: &base {a: 1}\nmerged: {<<: *base, b: 2}",
		"description: description: more", "description: [unclosed", "description: @bad", "description: *missing",
		"future: [one, true, null, 3]", "future: {x: one, y: [two, three]}",
		"name: true\nname: false", "yes: yes\nno: no\non: on\noff: off",
		"null: null\nnone: ~\nempty:", "value: 2026-10-06\ntime: 12:34:56",
		"n: 012\nhex: 0x12\noct: 0o12\nbinary: 0b11\nunderscore: 1_000",
		"n: .inf\nnegative: -.inf\nnan: .nan\nlarge: 1e999\nzero: -0",
		"description: !!str 42\nnum: !!int '10'", "value: !custom hello",
		"data: !!binary SGVsbG8=", "description: |\n  first\n\n  second\n",
		"description: >\n  first\n  second\n\n  third\n", "description: |2-\n  first\n  second\n",
		"description: \"line\\nnext\\tend\\x41\\u0042\\U0001F600\"",
		"description: 'it''s fine'", "description: \"\"", "description: false", "description: 0",
		"name: [one, null, [two, three], {x: 1}]", "\"2\": two\n\"1\": one\nx: last",
		"x: one\nx: two", "__proto__: {x: one}\nconstructor: retained",
		"[one, two]", "42", "future:\n  first: true\n  second:\n    - nested",
		"base: &base {a: 1, b: 2}\nmerged: {b: 3, <<: *base, c: 4}",
		"base: &base {a: 1, b: 2}\nmerged: {<<: *base, b: 3, c: 4}",
		"a: &a {first: 1, shared: one}\nb: &b {second: 2, shared: two}\nc: {<<: [*a, *b], last: 3}",
		"value: !!int word\nstring: !!str null\nbool: !!bool 'false'",
		"value: .Inf\nnan: .NaN\nbool: True\nother: FALSE\nupper: NULL",
		"a: +12\nb: .5\nc: 5.\nd: 1e-324\ne: +0x20\nf: 0xffffffffffffffffffff\ng: -0o10",
		"literal: '<<'\n'<<': retained\n[one, two]: array-key\ntrue: bool-key\nnull: null-key",
		"empty: {}\narray: []\nfalseString: 'false'\nnumberString: '12'",
		"emoji: \"\\ud83d\\ude00\"\nlone: \"\\ud800\"\nlow: \"\\udfff\"",
		"name: \"\\ud83d\\ude00\"\ndescription: accepted pair",
		"line: \"first\n  second\"\nplain: first\n  second\n\n  third",
		"max: 0x7fffffffffffffff\nunsigned: 0xffffffffffffffff\nover: 0x10000000000000000\nnegative: -0xffffffffffffffff",
		"max: 9223372036854775807\nunsigned: 18446744073709551615\nover: 18446744073709551616\nnegative: -18446744073709551616",
		"---\nextra: second-document", "x: \"\\0\\a\\b\\v\\f\\e\\N\\_\\L\\P\"",
	}
	for _, value := range values {
		out = append(out, "---\n"+value+"\n---\nbody 😀\n")
	}
	out = append(out, "--- \t\r\nname: test\r\n---\t\r\n \t\r\nbody")
	for _, prefix := range []string{"中文\n", "😀\n", "aaaa😀", "\uFEFF"} {
		out = append(out, prefix+"---\nname: 中文😀\n---\nbody")
	}
	return out
}

func TestActualSourceMarkdownFrontmatter(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-markdown-frontmatter.fixture.ts")
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	inputs := sourceCases()
	body, _ := json.Marshal(inputs)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_MARKDOWN_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source failed: %v %s", err, stderr.Bytes())
	}
	var expected []json.RawMessage
	if err = json.Unmarshal(stdout, &expected); err != nil {
		t.Fatalf("Source output: %v %s", err, stdout)
	}
	if len(expected) != len(inputs) {
		t.Fatalf("Source cases: %d", len(expected))
	}
	for i, input := range inputs {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			result, err := Parse(input)
			if err != nil {
				t.Fatalf("parse %q: %v\nSource=%s", input, err, expected[i])
			}
			actual, _ := json.Marshal(result)
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(expected[i], &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("input=%q\nGo=%s\nSource=%s", input, actual, expected[i])
			}
		})
	}
}
