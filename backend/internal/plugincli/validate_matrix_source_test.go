package plugincli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
)

func TestOriginalValidationStrictManifestContentAndPhysicalIO(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	paths, names := []string{}, []string{}
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	add := func(name, manifest string) string {
		p := filepath.Join(home, name, ".claude-plugin", "plugin.json")
		write(p, manifest)
		paths = append(paths, filepath.Dir(filepath.Dir(p)))
		names = append(names, name)
		return filepath.Dir(filepath.Dir(p))
	}
	valid := `{"name":"fixture","version":"1.0.0","description":"Actual fixture","author":{"name":"Fixture"}}`
	add("valid", valid)
	add("warnings", `{"name":"Upper_Name","category":"x","source":"./src","tags":[],"strict":false,"id":7}`)
	add("strict", `{"name":"fixture","typo":1,"version":7,"author":{"name":"Fixture","extra":"nested strip"}}`)
	add("traversal-before-schema", `{"name":"bad name","commands":["./safe..file.md",3],"agents":"../worker.md","skills":["../skill"],"future":true}`)
	add("manifest-components", `{"name":"fixture","commands":{"hello":{"source":"./hello.md","description":"d"}},"agents":["./worker.md"],"skills":"./skills/","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo x"}]}]},"mcpServers":{"fixture":{"command":"echo","args":[]}},"dependencies":["alpha@market"]}`)
	add("components-invalid", `{"name":"fixture","commands":{"hello":null},"hooks":{"Unknown":[{"hooks":[{"type":"command","command":"echo"}]}]},"lspServers":{"x":{"command":5,"extensionToLanguage":{".go":false}}}}`)
	for _, value := range []string{"null", "[]", "false", "12", `"primitive"`} {
		add("root-"+strings.Trim(value, `"`), value)
	}
	for i, syntax := range []string{"", "{", "{bad", `{"name":}`, `{"name":"x",}`, `[1,]`, "not-json", `{}x`, "\ufeff{}", `"bad\q"`, "01", "[", "tru"} {
		add("json-"+string(rune('a'+i)), syntax)
	}
	add("proto-key", `{"name":"fixture","__proto__":{"category":"x"}}`)
	add("constructor-key", `{"name":"fixture","constructor":1,"prototype":2}`)
	add("repeated-stray-key", `{"name":"fixture","category":"first","category":"last","future":"unknown"}`)
	add("non-ascii-name", `{"name":"插件","version":"1.0","description":"unicode","author":{"name":"Fixture"}}`)
	add("unknown-hook-inner", `{"name":"fixture","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo","future":"strip"}],"future":"strip"}]}}`)
	add("overflow", `{"name":"fixture","version":1e400,"future":-1e400}`)
	empty := filepath.Join(home, "empty-directory")
	if err = os.MkdirAll(empty, 0700); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, empty)
	names = append(names, "empty-directory")
	paths = append(paths, filepath.Join(home, "absent-plugin.json"), filepath.Join(home, "absent", "plugin.json"))
	names = append(names, "missing-unknown", "missing-known")
	custom := filepath.Join(home, "content-detect.json")
	write(custom, `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[],"future":7}`)
	paths = append(paths, custom)
	names = append(names, "content-detection")
	market := add("market-preferred", valid)
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[{"name":"same","source":"./plugins/same","version":"1.0"},{"name":"same","source":"./plugins/same","version":"1.0"}]}`)
	write(filepath.Join(market, "plugins", "same", ".claude-plugin", "plugin.json"), `{"name":"same","version":"2.0"}`)
	market = add("market-strict", valid)
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[{"name":"a","source":"../plugins/a","typo":1},{"name":"b","source":{"source":"git-subdir","url":"https://example.invalid/repo.git","path":"../x"}}],"typo":true}`)
	market = add("market-invalid-preferred", valid)
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), "{")
	market = add("market-directory-file", valid)
	if err = os.MkdirAll(filepath.Join(market, ".claude-plugin", "marketplace.json"), 0700); err != nil {
		t.Fatal(err)
	}
	contents := add("content-scan", valid)
	write(filepath.Join(contents, "skills", "ignored.md"), "invalid direct skill is not loaded")
	write(filepath.Join(contents, "skills", "alpha", "SKILL.md"), "---\ndescription: actual skill\n---\nClean")
	write(filepath.Join(contents, "skills", "beta", "SKILL.md"), "No frontmatter")
	write(filepath.Join(contents, "skills", "beta", "nested", "SKILL.md"), "Not scanned")
	if err = os.MkdirAll(filepath.Join(contents, "skills", "no-skill-file"), 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(contents, "agents", "nested", "bad.MD"), "---\ndescription: [a, b]\nname: 4\nallowed-tools: [Read, 7]\nshell: fish\n---\nBody")
	write(filepath.Join(contents, "agents", "ignored.txt"), "ignored")
	write(filepath.Join(contents, "commands", "clean.md"), "---\ndescription: false\nname: null\nallowed-tools: Read\nshell: ' PowerShell '\n---\nBody")
	write(filepath.Join(contents, "commands", "warning.md"), "---\nname: fixture\n---\nBody")
	write(filepath.Join(contents, "hooks", "hooks.json"), `{"description":"fixture","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo fixture"}]}]},"unknown":"strip"}`)
	for name, frontmatter := range map[string]string{
		"empty": "", "null": "null", "scalar": "scalar", "number": "42", "bool": "true", "sequence": "- a\n- b", "flow": "description: [unterminated", "escape": `description: "\q"`, "alias": "description: *missing", "recursive": "description: &x [*x]", "duplicate": "description: [bad]\ndescription: valid", "numeric": "description: .inf\nname: false\nallowed-tools: 4\nshell: []", "types": "description: {x: y}\nname: []\nallowed-tools: true\nshell: false", "nullfields": "description: null\nname: null\nallowed-tools: null\nshell: null", "alias-valid": "description: &x value\nname: *x", "merge": "defaults: &x {description: valid}\n<<: *x", "literal": "description: |\n  line\n  ---\nname: fixture", "preface": "description: fixture", "tab": "description:\tbad", "invalid-indent": "name: x\n bad: x\n  nope: x",
	} {
		dir := add("yaml-"+name, valid)
		content := "---\n" + frontmatter + "\n---\nBody"
		if name == "preface" {
			content = "preface\n" + content
		}
		write(filepath.Join(dir, "commands", "fixture.md"), content)
	}
	for name, frontmatter := range map[string]string{
		"tag-bool-invalid": "name: !!bool foo\ndescription: valid", "tag-int-invalid": "name: !!int invalid\ndescription: valid", "tag-null-invalid": "name: !!null x\ndescription: valid", "binary-prefix": "name: 0b10\ndescription: valid", "numeric-underscore": "name: 1_000\ndescription: valid", "uppercase-octal": "name: 0O77\ndescription: valid", "uppercase-hex": "name: 0XFF\ndescription: valid", "lowercase-octal": "name: 0o77\ndescription: valid", "lowercase-hex": "name: 0xff\ndescription: valid", "quoted-number": "name: !!int \"42\"\ndescription: valid", "unknown-tag": "name: !foo 42\ndescription: valid", "null-tag": "name: !!null null\ndescription: valid", "str-tag": "name: !!str false\ndescription: valid", "tag-time": "name: !!timestamp 2020-01-01\ndescription: valid", "unicode-trim": "description: valid\nshell: '\ufeffPowerShell\ufeff'", "at-sign": "description: @foo", "tab-indent": "description:\n\tbad", "unicode-escape": `description: "\uZZZZ"`, "flow-comma": "description: [a,,b]", "merge-precedence": "base: &x {description: from-merge}\n<<: *x\ndescription: valid", "merge-array": "a: &a {description: valid}\nb: &b {description: [invalid]}\n<<: [*a,*b]", "duplicate-array-first": "description: [bad]\ndescription: valid", "duplicate-array-last": "description: valid\ndescription: [bad]", "alias-tools": "description: valid\na: &a [Read]\nallowed-tools: *a", "shell-alias": "description: &a bash\nshell: *a", "bom-key": "\ufeffdescription: valid", "merge-ignored": "<<: [7]", "null-merge": "<<: null",
	} {
		dir := add("yaml-extra-"+name, valid)
		write(filepath.Join(dir, "commands", "fixture.md"), "---\n"+frontmatter+"\n---\nBody")
	}
	for name, frontmatter := range map[string]string{"quoted-merge": `"<<": {description: valid}`, "explicit-array-key": "? [description]\n: valid", "flow-array-key": "{[description]: valid}", "explicit-after-plain": "description: valid\n? name\n: fixture", "explicit-repeated": "? description\n: valid\n? name\n: fixture"} {
		dir := add("yaml-map-"+name, valid)
		write(filepath.Join(dir, "commands", "fixture.md"), "---\n"+frontmatter+"\n---\nBody")
	}
	for name, hooks := range map[string]string{"json": "{", "root": "null", "missing-hooks": `{"description":"x"}`, "invalid-command": `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":4}]}]}}`, "unknown-event": `{"hooks":{"Invalid":[{"hooks":[{"type":"command","command":"echo"}]}]}}`, "invalid-matcher": `{"hooks":{"Stop":[{"matcher":3,"hooks":[{"type":"http","url":"bad-url","headers":{"x":4}}]}]}}`} {
		dir := add("hooks-"+name, valid)
		write(filepath.Join(dir, "hooks", "hooks.json"), hooks)
	}
	// A symlinked file/dir is ignored by withFileTypes, while a speculative
	// SKILL.md directory must produce its real read failure.
	dir := add("physical-entry-kinds", valid)
	write(filepath.Join(dir, "actual.md"), "No metadata")
	if err = os.MkdirAll(filepath.Join(dir, "commands"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(dir, "actual.md"), filepath.Join(dir, "commands", "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "skills", "directory", "SKILL.md"), 0700); err != nil {
		t.Fatal(err)
	}
	locked := add("read-denied", valid)
	p := filepath.Join(locked, ".claude-plugin", "plugin.json")
	if err = os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0600) })
	dir = add("component-read-denied", valid)
	p = filepath.Join(dir, "agents", "denied.md")
	write(p, "No frontmatter")
	if err = os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	lockedComponent := p
	t.Cleanup(func() { _ = os.Chmod(lockedComponent, 0600) })
	dir = add("hooks-read-denied", valid)
	p = filepath.Join(dir, "hooks", "hooks.json")
	write(p, "{}")
	if err = os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	lockedHook := p
	t.Cleanup(func() { _ = os.Chmod(lockedHook, 0600) })
	dir = add("scan-denied", valid)
	p = filepath.Join(dir, "agents")
	if err = os.MkdirAll(p, 0); err != nil {
		t.Fatal(err)
	}
	lockedScan := p
	t.Cleanup(func() { _ = os.Chmod(lockedScan, 0700) })
	write(filepath.Join(home, "file-ancestor"), "regular file")
	paths = append(paths, filepath.Join(home, "file-ancestor", "plugin.json"))
	names = append(names, "unexpected-stat-enotdir")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugin-validate.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + root, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_PLUGIN_VALIDATE_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	payload, _ := json.Marshal(map[string]any{"paths": paths})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source validator", err, stderr.String())
	}
	var rows []struct {
		Manifest plugincli.ValidationResult   `json:"manifest"`
		Contents []plugincli.ValidationResult `json:"contents"`
		Error    string                       `json:"error"`
	}
	if err = json.Unmarshal(data, &rows); err != nil || len(rows) != len(paths) {
		t.Fatal("actual Source rows", len(rows), len(paths), err, string(data))
	}
	for i, p := range paths {
		t.Run(names[i], func(t *testing.T) {
			manifest, err := plugincli.ValidateManifest(ctx, p, home)
			if rows[i].Error != "" {
				if err == nil && manifest.FileType == "plugin" && filepath.Base(filepath.Dir(manifest.FilePath)) == ".claude-plugin" {
					_, err = plugincli.ValidatePluginContents(ctx, filepath.Dir(filepath.Dir(manifest.FilePath)))
				}
				if err == nil || err.Error() != rows[i].Error {
					t.Fatalf("Source error=%q Go=%v", rows[i].Error, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(manifest, rows[i].Manifest) {
				t.Fatalf("Source manifest=%s Go=%s", mustValidationJSON(rows[i].Manifest), mustValidationJSON(manifest))
			}
			contents := []plugincli.ValidationResult{}
			if manifest.FileType == "plugin" && filepath.Base(filepath.Dir(manifest.FilePath)) == ".claude-plugin" {
				contents, err = plugincli.ValidatePluginContents(ctx, filepath.Dir(filepath.Dir(manifest.FilePath)))
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(contents, rows[i].Contents) {
				t.Fatalf("Source contents=%s Go=%s", mustValidationJSON(rows[i].Contents), mustValidationJSON(contents))
			}
		})
	}
}
func mustValidationJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
