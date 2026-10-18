package skillcommands_test

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

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestNativePluginCommandDefinitionsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "work")
	config := filepath.Join(home, ".claude")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	market := filepath.Join(home, "market")
	write(filepath.Join(config, "settings.json"), `{"enabledPlugins":{"sample@mkt":true,"custom@mkt":true}}`)
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[{"name":"sample","source":"./sample"},{"name":"custom","source":"./custom"}]}`)
	known, _ := json.Marshal(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}})
	write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(known))
	write(filepath.Join(market, "sample", ".claude-plugin", "plugin.json"), `{"name":"sample","version":"1.0.0"}`)
	write(filepath.Join(market, "sample", "commands", "plain.md"), "---\ndescription: 12\nargument-hint: [x]\narguments: first second\nallowed-tools: 'Read, Bash(echo *)'\nwhen_to_use: use this\nversion: 2\neffort: high\nshell: PowerShell\n---\nUse $first and $ARGUMENTS.")
	write(filepath.Join(market, "sample", "commands", "leaf", "SKILL.md"), "---\nname: friendly\ndescription: Leaf command\nuser-invocable: false\n---\nLeaf.")
	write(filepath.Join(market, "sample", "commands", "leaf", "skip.md"), "Skip in Skill leaf.")
	write(filepath.Join(market, "sample", "commands", "leaf", "nested", "skip.md"), "Skip recursive Skill leaf.")
	write(filepath.Join(market, "sample", "commands", "upper.MD"), "Upper file.")
	write(filepath.Join(market, "sample", "skills", "first", "SKILL.md"), "---\nname: Friendly\ndescription: [invalid, description]\nallowed-tools: Read(${CLAUDE_PLUGIN_ROOT}/assets)\ndisable-model-invocation: true\n---\n# First Skill\nInstructions.")
	write(filepath.Join(market, "sample", "skills", "second", "SKILL.md"), "---\nname: sample:Second\nargument-hint: 12\nuser-invocable: 'false'\n---\nSecond.")
	write(filepath.Join(market, "sample", "skills", ".hidden", "SKILL.md"), "Hidden plugin Skill is discovered.")
	write(filepath.Join(market, "custom", ".claude-plugin", "plugin.json"), `{"name":"custom","version":"1.0.0","commands":{"named":{"source":"./extra.md","description":"Override description","argumentHint":"Override hint","allowedTools":["Read","Bash(echo *)"]},"inline":{"content":"---\nname: inline label\nversion: 2\n---\nInline instructions.","description":" "}},"skills":["./direct"]}`)
	write(filepath.Join(market, "custom", "extra.md"), "---\ndescription: Original description\nargument-hint: Original hint\nallowed-tools: Write\n---\nExtra instructions.")
	write(filepath.Join(market, "custom", "direct", "SKILL.md"), "---\nname: custom:Direct\ndescription: Direct skill\n---\nDirect instructions.")

	// Materialize with the actual native producer; Source independently reads
	// its resulting installed artifacts, config and component declarations.
	native, err := newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{
		Environment: map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": filepath.Join(home, "tmp")}, CWD: cwd, Platform: runtime.GOOS,
		ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(storage.Options{})
	settings, err := clibootstrap.NewNativeProcessSettings(native, runtime.GOOS, clibootstrap.DefaultState(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Close(context.Background()) })
	plugins, err := settings.LoadPlugins(context.Background(), "", true)
	if err != nil || len(plugins.Errors) != 0 {
		t.Fatalf("actual installed plugin producer: %v %v", err, plugins.Errors)
	}
	installed, err := pluginregistry.NewInstalledRegistry(pluginregistry.InstalledOptions{PluginsDir: filepath.Join(config, "plugins"), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	for _, plugin := range plugins.Enabled {
		if err := installed.Add(context.Background(), plugin.Source, "user", plugin.Path, plugin.Manifest, nil); err != nil {
			t.Fatal(err)
		}
	}
	plugins, err = settings.RefreshInstalledPluginsForProject(context.Background(), cwd)
	if err != nil || len(plugins.Errors) != 0 {
		t.Fatalf("actual project plugin producer: %v %v", err, plugins.Errors)
	}
	s, err := settings.SkillCommands(clibootstrap.NativeSkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]any{"commands": definitionMetadata(s.PluginCommands(context.Background(), plugins.Enabled)), "skills": definitionMetadata(s.PluginSkills(context.Background(), plugins.Enabled))}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-plugin-commands.fixture.ts")
	input, _ := json.Marshal(map[string]string{"cwd": cwd})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir, cmd.Stdin = home, bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_PLUGIN_COMMAND_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source plugin commands: %v %s", err, stderr.Bytes())
	}
	var source map[string]any
	if err := json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if errors, ok := source["errors"].([]any); !ok || len(errors) != 0 {
		t.Fatal("Source actual plugin loader failed", string(output))
	}
	delete(source, "errors")
	gotJSON, _ := json.Marshal(actual)
	var got any
	_ = json.Unmarshal(gotJSON, &got)
	// Source walks sibling directories concurrently and appends on I/O
	// completion. Compare every discovered definition, with its full metadata
	// and internal array order intact. This does not claim completion-order
	// equivalence or prove downstream Prompt/tool-schema ordering.
	index := func(value any) map[string]any {
		result := map[string]any{}
		for kind, definitions := range value.(map[string]any) {
			for _, entry := range definitions.([]any) {
				name := kind + "/" + entry.(map[string]any)["name"].(string)
				if _, duplicate := result[name]; duplicate {
					t.Fatalf("duplicate discovered definition: %s", name)
				}
				result[name] = entry
			}
		}
		return result
	}
	if !reflect.DeepEqual(index(got), index(source)) {
		t.Fatalf("actual plugin command metadata/discovery differs: Go %s\nSource %s", gotJSON, output)
	}
}

func definitionMetadata(definitions []skillcommands.Definition) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	for _, definition := range definitions {
		out = append(out, definition.Metadata())
	}
	return out
}
