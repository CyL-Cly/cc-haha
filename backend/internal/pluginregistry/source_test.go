package pluginregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

type createCase struct {
	Path     string `json:"path"`
	Source   string `json:"source"`
	Enabled  bool   `json:"enabled"`
	Fallback string `json:"fallback"`
	Strict   bool   `json:"strict"`
}

func source(t *testing.T, home string, input any) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-plugin-loader.fixture.ts")
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(input)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_PLUGIN_LOADER_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ALLOW_PROTOCOL=file"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source: %v %s", err, stderr.Bytes())
	}
	return output
}
func compare(t *testing.T, actual any, expected json.RawMessage) {
	t.Helper()
	data, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal(data, &a)
	if err = json.Unmarshal(expected, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go=%s\nSource=%s", data, expected)
	}
}
func TestNativePluginComponentsAgainstProductionSource(t *testing.T) {
	home := t.TempDir()
	cases := []createCase{}
	manifests := []string{
		"", `{"name":"sample","description":"Description","future":"strip","settings":{"agent":"Plan","future":true}}`,
		`{"name":"sample","commands":["./present.md","./missing.md"],"agents":"./missing-agent.md","skills":["./extra","./missing-skill"],"workflows":"./missing-workflow","outputStyles":"./missing-output"}`,
		`{"name":"sample","commands":{"read":{"source":"./present.md","description":"Read"},"inline":{"content":"Inline"},"missing":{"source":"./missing.md"}}}`,
		`{"name":"sample","hooks":["./hooks/hooks.json","./extra-hooks.json",{"Stop":[{"hooks":[{"type":"command","command":"inline"}]}]}]}`,
		`{"name":"sample","hooks":"./missing.json"}`, `{"name":"sample","hooks":"./bad-hooks.json"}`,
		`{"name":"sample","settings":{"agent":"Plan"}}`, `{"name":"sample","commands":{},"skills":[],"outputStyles":[]}`,
		`{"name":"invalid name"}`, `{"name":"sample","userConfig":{"key":{"type":"string","title":"Key","future":true}}}`,
		`{"name":`, "null", `{"name":"sample","hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"echo offline","future":true}]}]}}`,
	}
	for i, manifest := range manifests {
		root := filepath.Join(home, fmt.Sprintf("plugin-%02d", i))
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{"commands", "agents", "skills", "output-styles", "workflows", "extra"} {
			if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
				t.Fatal(err)
			}
		}
		put(t, filepath.Join(root, "present.md"), "Command body")
		put(t, filepath.Join(root, "hooks", "hooks.json"), `{"description":"Hook file","hooks":{"Stop":[{"hooks":[{"type":"command","command":"standard"}]}]}}`)
		put(t, filepath.Join(root, "extra-hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"extra"}]}]}}`)
		put(t, filepath.Join(root, "bad-hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"unknown"}]}]}}`)
		if manifest != "" {
			put(t, filepath.Join(root, ".claude-plugin", "plugin.json"), manifest)
		}
		if i == 1 {
			put(t, filepath.Join(root, "settings.json"), `{"agent":"Explore","future":true}`)
		}
		if i == 7 {
			put(t, filepath.Join(root, "settings.json"), `{"agent":false}`)
		}
		cases = append(cases, createCase{root, "sample@market", i%2 == 0, "fallback", true})
	}
	cases = append(cases, createCase{cases[4].Path, "sample@market", true, "fallback", false})
	var expected struct{ Created []json.RawMessage }
	if err := json.Unmarshal(source(t, home, map[string]any{"create": cases}), &expected); err != nil {
		t.Fatal(err)
	}
	creator, err := NewCreator(nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range cases {
		t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
			plugin, errs, err := creator.Create(context.Background(), item.Path, item.Source, item.Enabled, item.Fallback, item.Strict)
			if err != nil {
				compare(t, map[string]any{"error": err.Error()}, expected.Created[i])
			} else {
				compare(t, map[string]any{"result": map[string]any{"plugin": plugin, "errors": errs}}, expected.Created[i])
			}
		})
	}
}
func TestDependenciesAndManagedInlinePrecedenceAgainstSource(t *testing.T) {
	home := t.TempDir()
	plugin := func(name, source string, enabled bool, deps ...string) Loaded {
		return Loaded{Name: name, Source: source, Repository: source, Path: "/fixture/" + name, Enabled: enabled, Manifest: protocol.NewJSONObject(field("name", name), field("dependencies", deps))}
	}
	merged := []struct {
		Session     []Loaded `json:"session"`
		Marketplace []Loaded `json:"marketplace"`
		Builtin     []Loaded `json:"builtin"`
		Managed     []string `json:"managedNames"`
	}{
		{[]Loaded{plugin("a", "a@inline", true)}, []Loaded{plugin("a", "a@mkt", true), plugin("b", "b@mkt", false)}, nil, nil},
		{[]Loaded{plugin("a", "a@inline", true), plugin("c", "c@inline", true)}, []Loaded{plugin("a", "a@mkt", false)}, []Loaded{plugin("builtin", "builtin@builtin", true)}, []string{"a"}},
	}
	for i := range merged {
		if merged[i].Builtin == nil {
			merged[i].Builtin = []Loaded{}
		}
	}
	deps := [][]Loaded{
		{plugin("a", "a@mkt", true, "b"), plugin("b", "b@mkt", false)},
		{plugin("a", "a@mkt", true, "b"), plugin("b", "b@mkt", true, "c")},
		{plugin("a", "a@mkt", true, "b"), plugin("b", "b@mkt", true, "a")},
		{plugin("a", "a@inline", true, "b"), plugin("b", "b@one", true, "missing"), plugin("b", "b@two", true)},
		{plugin("a", "a@inline", true, "b"), plugin("b", "b@one", true, "missing")},
	}
	var expected struct {
		Merged       []json.RawMessage
		Dependencies []json.RawMessage
	}
	if err := json.Unmarshal(source(t, home, map[string]any{"merge": merged, "dependencies": deps}), &expected); err != nil {
		t.Fatal(err)
	}
	for i, item := range merged {
		t.Run(fmt.Sprintf("merge_%d", i), func(t *testing.T) {
			names := map[string]bool{}
			for _, name := range item.Managed {
				names[name] = true
			}
			plugins, errors := Merge(item.Session, item.Marketplace, item.Builtin, names)
			compare(t, map[string]any{"plugins": plugins, "errors": errors}, expected.Merged[i])
		})
	}
	for i, items := range deps {
		t.Run(fmt.Sprintf("deps_%d", i), func(t *testing.T) {
			demoted, errors := VerifyAndDemote(items)
			compare(t, map[string]any{"demoted": demoted, "errors": errors}, expected.Dependencies[i])
		})
	}
}
