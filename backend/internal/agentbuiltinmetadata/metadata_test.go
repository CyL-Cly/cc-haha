package agentbuiltinmetadata

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func source(t *testing.T, input any) []json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agents-api.fixture.ts")
	command := exec.Command(bun, "--no-env-file", "run", fixture)
	command.Dir = home
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=test", "USER_TYPE=external", "CLAUDE_CODE_ENTRYPOINT=claude-desktop", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_AGENTS_FIXTURE=1"}
	raw, _ := json.Marshal(input)
	command.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Actual Source builtin metadata %v %s", err, stderr.String())
	}
	var rows []json.RawMessage
	if err = json.Unmarshal(output, &rows); err != nil {
		t.Fatalf("Invalid Source metadata %v %s", err, output)
	}
	return rows
}
func object(value any) protocol.JSONObject {
	raw, _ := json.Marshal(value)
	var out protocol.JSONObject
	if err := out.UnmarshalJSON(raw); err != nil {
		panic(err)
	}
	return out
}
func equal(t *testing.T, a, b any) {
	t.Helper()
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	var x, y any
	_ = json.Unmarshal(rawA, &x)
	_ = json.Unmarshal(rawB, &y)
	if !reflect.DeepEqual(x, y) {
		t.Fatalf("Source %s\nGo %s", rawA, rawB)
	}
}
func TestBuiltinDeclarationsAndZeroArgPromptsAgainstActualSource(t *testing.T) {
	original := source(t, map[string]any{"cases": []any{map[string]any{"action": "stock"}}})
	rows := []any{}
	for _, definition := range Stock() {
		rows = append(rows, map[string]any{"fields": definition.Fields, "systemPrompt": definition.SystemPrompt, "promptArity": definition.PromptArity})
	}
	var value any
	_ = json.Unmarshal(original[0], &value)
	equal(t, value, rows)
	first := Stock()
	first[0].Fields.Set("model", []byte("\"modified-private-copy\""))
	if Stock()[0].Fields.Get("model") != nil {
		t.Fatal("Stock definitions must remain immutable across consumers")
	}
}
func TestGuideDynamicConfigurationAgainstActualSource(t *testing.T) {
	settings := object(map[string]any{"future": map[string]any{"keep": 1}, "language": "zh-CN", "env": map[string]any{"SAFE_FAKE": "example"}})
	contexts := []GuideContext{
		{},
		{Commands: []protocol.JSONObject{object(map[string]any{"type": "prompt", "name": "private-skill", "description": "fixture skill"})}},
		{Commands: []protocol.JSONObject{object(map[string]any{"type": "prompt", "source": "plugin", "name": "plugin:check", "description": "fixture plugin"}), object(map[string]any{"type": "local", "name": "ignored"})}},
		{Agents: []protocol.JSONObject{object(map[string]any{"source": "projectSettings", "agentType": "review", "whenToUse": "fixture reviewer"}), object(map[string]any{"source": "built-in", "agentType": "Explore"})}},
		{MCPServers: []protocol.JSONObject{object(map[string]any{"name": "private-loopback"})}},
		{Commands: []protocol.JSONObject{object(map[string]any{"type": "prompt"})}, Agents: []protocol.JSONObject{object(map[string]any{"agentType": "future", "whenToUse": "future"})}},
		{Using3P: true, IssuesExplainer: "https://fixture.invalid/feedback"},
	}
	cases := []any{}
	for _, context := range contexts {
		cases = append(cases, map[string]any{"action": "guide", "commands": context.Commands, "agents": context.Agents, "mcpServers": context.MCPServers, "using3P": context.Using3P, "issuesExplainer": context.IssuesExplainer})
	}
	observed := source(t, map[string]any{"settings": settings, "cases": cases})
	for i, context := range contexts {
		var expected struct {
			Prompt   string
			Settings protocol.JSONObject
		}
		if err := json.Unmarshal(observed[i], &expected); err != nil {
			t.Fatal(err)
		}
		context.Settings = expected.Settings
		if got := GuidePrompt(context); got != expected.Prompt {
			t.Fatalf("case%d Source=%q\nGo=%q", i, expected.Prompt, got)
		}
	}
}
