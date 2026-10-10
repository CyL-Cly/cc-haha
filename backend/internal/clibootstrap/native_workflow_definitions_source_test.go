package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestNativeWorkflowDefinitionsAgainstActualSourceAndPhysicalSaveDelete(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":true},"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"future":{"keep":true}}`)
	f.write(t, filepath.Join(f.plugin, "workflows", "a.js"), `export const meta={name:'plugin-default',description:'Default plugin directory'};return 1`)
	f.write(t, filepath.Join(f.plugin, "one", "a.js"), `export const meta={name:'collision',description:'First declared path'};return 1`)
	f.write(t, filepath.Join(f.plugin, "two", "a.js"), `export const meta={name:'collision',description:'Last declared path wins'};return 1`)
	f.write(t, filepath.Join(f.plugin, ".claude-plugin", "plugin.json"), `{"name":"sample","workflows":["./one/a.js","./two"]}`)
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	owner := cooperative.New()
	defer owner.Close()
	p, e := NewNativeProcessSettings(f.n, runtime.GOOS, DefaultState())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close(context.Background())
	service, e := p.WorkflowDefinitions(owner, f.cwd)
	if e != nil {
		t.Fatal(e)
	}
	type operation struct{ Kind, Name, Script, Scope, CWD string }
	script := `export const meta={name:'actual-native',description:'Saved original workflow',whenToUse:'when testing',phases:[{title:'first',detail:'original'}]};
return await agent('Never executed during definition API')`
	operations := []operation{{Kind: "get", Name: "sample:collision"}, {Kind: "get", Name: "sample:plugin-default"}, {Kind: "validate", Script: script}, {Kind: "validate", Script: `export const meta={name:'bad syntax',description:'invalid'};return 1`}, {Kind: "validate", Script: `export const meta={name:'warning',description:'warn'};return Date.now()`}, {Kind: "save", Script: script}, {Kind: "get", Name: "actual-native"}, {Kind: "list"}, {Kind: "save", Script: script, Name: "renamed-original", Scope: "project"}, {Kind: "get", Name: "renamed-original"}, {Kind: "save", Script: script, Name: "help"}, {Kind: "save", Script: script, Name: "\ufeffhelp\ufeff"}, {Kind: "save", Script: script, Name: "\u0085help\u0085"}, {Kind: "get", Name: "missing-original"}, {Kind: "delete", Name: "../bad"}, {Kind: "delete", Name: "actual-native"}, {Kind: "get", Name: "actual-native"}, {Kind: "delete", Name: "actual-native"}, {Kind: "delete", Name: "renamed-original", Scope: "project"}}
	inputOps := []map[string]any{}
	actual := []map[string]any{}
	for _, op := range operations {
		scope := op.Scope
		if scope == "" {
			scope = "user"
		}
		var result any
		var e error
		switch op.Kind {
		case "validate":
			result, e = service.Validate(context.Background(), quoteMain(op.Script))
		case "save":
			var name json.RawMessage
			if op.Name != "" {
				name = quoteMain(op.Name)
			}
			result, e = service.SaveDefinition(context.Background(), quoteMain(op.Script), scope, nil, name)
		case "get":
			result, e = service.GetDefinition(context.Background(), op.Name, nil)
		case "list":
			result, e = service.ListDefinitions(context.Background(), nil)
		case "delete":
			e = service.DeleteDefinition(context.Background(), op.Name, scope, nil)
		}
		if e != nil {
			var api *workflow.APIError
			if !errors.As(e, &api) {
				t.Fatal(e)
			}
			actual = append(actual, map[string]any{"status": api.Status, "code": api.Code, "error": api.Message})
		} else {
			actual = append(actual, map[string]any{"result": result})
		}
		row := map[string]any{"kind": op.Kind}
		if op.Script != "" {
			row["script"] = op.Script
		}
		if op.Name != "" {
			row["name"] = op.Name
		}
		if op.Scope != "" {
			row["scope"] = op.Scope
		}
		inputOps = append(inputOps, row)
	}
	if _, e := os.Stat(filepath.Join(f.config, "workflows", "actual-native.js")); !os.IsNotExist(e) {
		t.Fatal("native delete did not remove physical user workflow", e)
	}
	if _, e := os.Stat(filepath.Join(f.cwd, ".claude", "workflows", "renamed-original.js")); !os.IsNotExist(e) {
		t.Fatal("native project delete did not remove physical workflow", e)
	}
	sourceHome := t.TempDir()
	sourceHome, _ = filepath.EvalSymlinks(sourceHome)
	_ = os.MkdirAll(filepath.Join(sourceHome, "project", ".git"), 0700)
	_ = os.WriteFile(filepath.Join(sourceHome, "project", ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0600)
	_ = os.MkdirAll(filepath.Join(sourceHome, "tmp"), 0700)
	_ = os.MkdirAll(filepath.Join(sourceHome, ".git"), 0700)
	_ = os.WriteFile(filepath.Join(sourceHome, ".git", "HEAD"), []byte("ref: refs/heads/fixture-home\n"), 0600)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	sourceFiles := []map[string]any{{"path": ".claude/settings.json", "text": `{"enabledPlugins":{"sample@mkt":true},"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"future":{"keep":true}}`}, {"path": "project/.claude/settings.json", "text": `{}`}, {"path": "project/.git/HEAD", "text": "ref: refs/heads/main\n"}}
	for _, relative := range []string{".claude/plugins/known_marketplaces.json", "market/.claude-plugin/marketplace.json", "market/plugins/sample/.claude-plugin/plugin.json", "market/plugins/sample/workflows/a.js", "market/plugins/sample/one/a.js", "market/plugins/sample/two/a.js"} {
		raw, e := os.ReadFile(filepath.Join(f.home, relative))
		if e != nil {
			t.Fatal(e)
		}
		sourceFiles = append(sourceFiles, map[string]any{"path": relative, "text": strings.ReplaceAll(string(raw), f.home, "<private-home>")})
	}
	input, _ := json.Marshal(map[string]any{"homeDir": sourceHome, "operations": inputOps, "files": sourceFiles, "inspectPluginCache": true})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = sourceHome
	cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CLAUDE_CODE_TMPDIR=" + filepath.Join(sourceHome, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("Source %v %.2000s %.2000s", e, stdout.String(), stderr.String())
	}
	var source struct {
		Observations []json.RawMessage
		PluginCache  struct {
			Keys           []string
			Enabled        int
			PluginsPresent bool
		}
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Capabilities.Blocked) > 0 || len(source.Observations) != len(actual) {
		t.Fatal(e, source.Capabilities.Blocked, len(source.Observations), stderr.String())
	}
	if source.PluginCache.Enabled != 1 || source.PluginCache.PluginsPresent || !reflect.DeepEqual(source.PluginCache.Keys, []string{"enabled", "disabled", "errors"}) {
		t.Fatal("actual Source plugin cache shape changed", source.PluginCache)
	}
	normalize := func(raw []byte, home string) any {
		raw = bytes.ReplaceAll(raw, []byte(home), []byte("<private-home>"))
		var result any
		if e := json.Unmarshal(raw, &result); e != nil {
			t.Fatal(e, string(raw))
		}
		return result
	}
	for i, row := range actual {
		raw, _ := json.Marshal(row)
		if !reflect.DeepEqual(normalize(raw, f.home), normalize(source.Observations[i], sourceHome)) {
			t.Fatal(fmt.Sprintf("Source definitions mismatch%d %s\nGo=%s\nSource=%s", i, operations[i].Kind, strings.ReplaceAll(string(raw), f.home, "<private-home>"), strings.ReplaceAll(string(source.Observations[i]), sourceHome, "<private-home>")))
		}
	}
	before, _ := os.ReadFile(filepath.Join(f.config, "settings.json"))
	if !bytes.Contains(before, []byte(`"future":{"keep":true}`)) {
		t.Fatal("definition APIs rewrote unknown settings")
	}
}
