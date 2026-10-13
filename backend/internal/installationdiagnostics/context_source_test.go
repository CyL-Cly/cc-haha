package installationdiagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDoctorContextWarningsAgainstActualSourceFunctions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cases := []map[string]any{{}}
	for _, length := range []int{39999, 40000, 40001, 80005} {
		cases = append(cases, map[string]any{"memory": []any{map[string]any{"path": "/one/CLAUDE.md", "content": strings.Repeat("x", length)}, map[string]any{"path": "/two/CLAUDE.md", "content": strings.Repeat("😀", length/2)}}})
	}
	for _, length := range []int{59990, 60000, 60010} {
		list := []any{}
		for i := 0; i < 7; i++ {
			list = append(list, map[string]any{"agentType": string(rune('a' + i)), "whenToUse": strings.Repeat("d", length/7), "source": "userSettings"})
		}
		list = append(list, map[string]any{"agentType": "builtin", "whenToUse": strings.Repeat("d", length), "source": "built-in"})
		cases = append(cases, map[string]any{"agents": list})
	}
	for _, source := range ruleSources {
		for _, sandbox := range []bool{false, true} {
			for _, deny := range []bool{false, true} {
				permission := map[string]any{"alwaysAllowRules": map[string]any{"userSettings": []string{"Bash(echo \\(one\\))", "Read(/tmp/**)", "Read"}}, "alwaysAskRules": map[string]any{source: []string{"Bash", "Read(*)"}}, "alwaysDenyRules": map[string]any{}}
				if deny {
					permission["alwaysDenyRules"] = map[string]any{"policySettings": []string{"Bash"}}
				}
				cases = append(cases, map[string]any{"permission": permission, "sandbox": sandbox})
			}
		}
	}
	for _, tokens := range []float64{0, 25500, 25501, 50001} {
		for _, deferred := range []bool{false, true} {
			tools := []any{}
			for i := 0; i < 7; i++ {
				tools = append(tools, map[string]any{"name": "mcp__" + string(rune('a'+i)) + "__t", "description": strings.Repeat("describe", i+1), "schema": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}, "alwaysLoad": i < 3})
			}
			cases = append(cases, map[string]any{"tools": tools, "tokens": tokens, "deferred": deferred})
		}
	}
	cases = append(cases, map[string]any{"tools": []any{map[string]any{"name": "mcp__failed__t", "description": "native peer"}}, "countError": true})
	input, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-doctor-context.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	child.Stdin = bytes.NewReader(input)
	output, e := child.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var expected []struct {
		Result     json.RawMessage
		TokenCalls [][]string
	}
	if e = json.Unmarshal(output, &expected); e != nil {
		t.Fatal(e, string(output))
	}
	for index, row := range cases {
		in := ContextInput{Permission: protocol.NewJSONObject()}
		in.SandboxAutoAllow = row["sandbox"] == true
		in.Deferred = row["deferred"] == true
		if value, ok := row["memory"]; ok {
			raw, _ := json.Marshal(value)
			_ = json.Unmarshal(raw, &in.Memory)
		}
		if value, ok := row["agents"].([]any); ok {
			for _, raw := range value {
				agent := raw.(map[string]any)
				in.Agents = append(in.Agents, agentcatalog.Definition{Name: agent["agentType"].(string), Description: agent["whenToUse"].(string), Source: agent["source"].(string)})
			}
		}
		if value, ok := row["permission"]; ok {
			raw, _ := json.Marshal(value)
			_ = in.Permission.UnmarshalJSON(raw)
		}
		if value, ok := row["tools"].([]any); ok {
			for _, raw := range value {
				tool := raw.(map[string]any)
				schema := protocol.NewJSONObject()
				input := tool["schema"]
				if input == nil {
					input = map[string]any{}
				}
				data, _ := json.Marshal(input)
				schema.Set("input_schema", data)
				in.MCP = append(in.MCP, MCPTool{Name: tool["name"].(string), Description: tool["description"].(string), Schema: schema, AlwaysLoad: tool["alwaysLoad"] == true})
			}
		}
		calls := 0
		in.Count = func([]protocol.JSONObject) (*float64, error) {
			calls++
			if row["countError"] == true {
				return nil, errors.New("native counter failure")
			}
			value, _ := row["tokens"].(float64)
			return &value, nil
		}
		actual := Context(in)
		gotRaw, _ := json.Marshal(actual)
		var got, want any
		_ = json.Unmarshal(gotRaw, &got)
		_ = json.Unmarshal(expected[index].Result, &want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("row%d Source%s Go%s", index, expected[index].Result, gotRaw)
		}
		if calls != len(expected[index].TokenCalls) {
			t.Fatal("actual counter consumer cardinality differs", index, calls, expected[index].TokenCalls)
		}
	}
}
