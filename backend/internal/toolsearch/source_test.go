package toolsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func source(t *testing.T, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tool-search.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TOOL_SEARCH_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw(input), &value)
	value["homeDir"] = quoted(home)
	cmd.Stdin = bytes.NewReader(raw(value))
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var result protocol.JSONObject
	if e := result.UnmarshalJSON(output); e != nil {
		t.Fatalf("Source JSON %v %.1500s", e, output)
	}
	capabilities, _ := result.Object("capabilities")
	var blocked []string
	_ = json.Unmarshal(capabilities.Get("blocked"), &blocked)
	if len(blocked) > 0 {
		t.Fatal("Source unowned access", blocked)
	}
	return result
}
func equalJSON(a, b json.RawMessage) bool {
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	return reflect.DeepEqual(x, y)
}
func input(text string) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON([]byte(text))
	return out
}

func TestActualSourceToolSearchScoringSelectionSchemaInputsAndDescriptionCache(t *testing.T) {
	tools := []protocol.JSONObject{
		input(`{"name":"ToolSearch","prompt":"Search","shouldDefer":true}`),
		input(`{"name":"Read","aliases":["ReadAlias"],"prompt":"Read a file","searchHint":"read text files"}`),
		input(`{"name":"NotebookEdit","shouldDefer":true,"prompt":"Read or write notebook cells","searchHint":"edit Jupyter notebooks"}`),
		input(`{"name":"mcp__slack__send_message","isMcp":true,"prompt":"Send a message to a channel","searchHint":"send slack messages"}`),
		input(`{"name":"mcp__slack__read_channel","isMcp":true,"prompt":"Read a Slack channel"}`),
		input(`{"name":"mcp__github__create_issue","isMcp":true,"prompt":"Create issues and send notifications"}`),
		input(`{"name":"mcp__always__read","isMcp":true,"alwaysLoad":true,"prompt":"Always loaded"}`),
		input(`{"name":"mcp__unicode__İΣ𐐀","isMcp":true,"prompt":"İSTANBUL ΣΟΣ\ud800","searchHint":"abc中文def"}`),
		input(`{"name":"mcp__space tool__action","isMcp":true,"prompt":"Space action"}`),
	}
	inputs := []string{`{"query":"select:ReadAlias,NotebookEdit,ReadAlias,missing"}`, `{"query":"SELECT:mcp__slack__send_message"}`, `{"query":"select:Read\n"}`, `{"query":"select:Read\r\n"}`, `{"query":"select:Read\nmissing"}`, `{"query":"read"}`, `{"query":"mcp__slack"}`, `{"query":"+slack send"}`, `{"query":"notebook jupyter"}`, `{"query":"+channel +read"}`, `{"query":"mcp__"}`, `{"query":""}`, `{"query":"+"}`, `{"query":"中文"}`, `{"query":"istanbul"}`, `{"query":"i̇stanbul"}`, `{"query":"σος"}`, `{"query":"\ud800"}`, `{"query":"space"}`, `{"query":"read","max_results":0}`, `{"query":"read","max_results":-1}`, `{"query":"read","max_results":2.9}`, `{"query":"mcp__slack","max_results":0}`, `{"query":"nothing","max_results":999999999999999999999}`, `{"query":"read","max_results":"5"}`, `{"query":null}`, `{"query":"read","future":true}`}
	rows := []map[string]any{}
	for _, in := range inputs {
		rows = append(rows, map[string]any{"tools": tools, "input": in, "pending": []string{"late-one", "late-two"}})
	}
	changed := append([]protocol.JSONObject{}, tools...)
	changed = append(changed, input(`{"name":"ExtraDeferred","shouldDefer":true,"prompt":"Read extra"}`))
	rows = append(rows, map[string]any{"tools": changed, "input": `{"query":"read"}`})
	original := source(t, map[string]any{"rows": rows})
	var observations []protocol.JSONObject
	_ = json.Unmarshal(original.Get("observations"), &observations)
	if len(observations) != len(rows) {
		t.Fatal("Source count", len(observations))
	}
	s := New()
	for i, row := range rows {
		trace := []string{}
		decls := row["tools"].([]protocol.JSONObject)
		native := []Tool{}
		for _, decl := range decls {
			d := decl.Clone()
			var aliases []json.RawMessage
			_ = json.Unmarshal(d.Get("aliases"), &aliases)
			native = append(native, Tool{Name: d.Get("name"), Aliases: aliases, IsMCP: string(d.Get("isMcp")) == "true", AlwaysLoad: string(d.Get("alwaysLoad")) == "true", ShouldDefer: string(d.Get("shouldDefer")) == "true", SearchHint: d.Get("searchHint"), Prompt: func(context.Context) (json.RawMessage, error) {
				trace = append(trace, d.String("name"))
				return d.Get("prompt"), nil
			}})
		}
		if observations[i].Get("issues") != nil {
			continue
		} // parser parity belongs to the original native schema stage below.
		in := input(row["input"].(string))
		if in.Get("max_results") == nil {
			in.Set("max_results", raw(5))
		}
		in.Delete("future")
		pending := []json.RawMessage{quoted("late-one"), quoted("late-two")}
		if row["pending"] == nil {
			pending = nil
		}
		data, e := s.Call(context.Background(), in, native, pending)
		if e != nil {
			t.Fatal(e)
		}
		mapped, e := MapResult(data, quoted("tool-id"))
		if e != nil {
			t.Fatal(e)
		}
		actual := protocol.NewJSONObject(protocol.JSONField{Name: "parsed", Value: raw(in)}, protocol.JSONField{Name: "data", Value: raw(data)}, protocol.JSONField{Name: "mapped", Value: raw(mapped)}, protocol.JSONField{Name: "trace", Value: raw(trace)})
		if !equalJSON(raw(actual), raw(observations[i])) {
			t.Errorf("case%d Go=%s Source=%s", i, raw(actual), raw(observations[i]))
		}
	}
}

func TestActualSourceToolSearchGatesRespectExplicitProxyBetaKillSwitchAndAutoEdges(t *testing.T) {
	rows := []map[string]any{}
	for _, value := range []string{"", "0", "false", " no ", "off", "1", "TRUE", " yes ", "on", "auto", "auto:0", "auto:100", "auto:101", "auto:-1", "auto:99suffix", "auto: 100tail", "auto:+99", "auto:invalid", "Auto:0"} {
		for _, base := range []string{"", "http://127.0.0.1:1", "https://api.anthropic.com:443/v1", "https://api.anthropic.com:444", "http://api.anthropic.com", "malformed"} {
			rows = append(rows, map[string]any{"env": map[string]string{"ENABLE_TOOL_SEARCH": value, "ANTHROPIC_BASE_URL": base}, "model": "fixture-model"})
		}
	}
	for _, env := range []map[string]string{{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": " yes ", "ENABLE_TOOL_SEARCH": "true"}, {"USER_TYPE": "ant", "ANTHROPIC_BASE_URL": "https://api-staging.anthropic.com"}, {"CLAUDE_CODE_USE_BEDROCK": "true", "ANTHROPIC_BASE_URL": "malformed"}, {"CLAUDE_CODE_USE_VERTEX": "1"}, {"CLAUDE_CODE_USE_FOUNDRY": "on"}, {"CLAUDE_CODE_USE_AZURE_OPENAI": "yes"}} {
		for _, model := range []string{"claude-haiku-4-5", "CLAUDE-HAIKU-FUTURE", "deepseek-flash"} {
			rows = append(rows, map[string]any{"env": env, "model": model})
		}
	}
	original := source(t, map[string]any{"gates": rows})
	var gates []protocol.JSONObject
	_ = json.Unmarshal(original.Get("gates"), &gates)
	if len(gates) != len(rows) {
		t.Fatal("Source count", len(gates))
	}
	s := New()
	for i, row := range rows {
		env := row["env"].(map[string]string)
		supports, e := s.SupportsModel(row["model"].(string), GateInputs{Environment: env})
		if e != nil {
			t.Fatal(e)
		}
		actual := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoted(Mode(env))}, protocol.JSONField{Name: "optimistic", Value: raw(Optimistic(env))}, protocol.JSONField{Name: "modelSupports", Value: raw(supports)}, protocol.JSONField{Name: "prompt", Value: quoted(Prompt(env["USER_TYPE"] == "ant"))})
		if !equalJSON(raw(actual), raw(gates[i])) {
			t.Error(fmt.Sprintf("gate%d Go=%s Source=%s", i, raw(actual), raw(gates[i])))
		}
	}
}
