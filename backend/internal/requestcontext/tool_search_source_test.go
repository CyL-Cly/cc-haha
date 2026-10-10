package requestcontext

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
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestActualSourceToolSearchReferenceAvailabilityCallerAndModelSwitchNormalization(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	toolRows := []map[string]any{{"name": "ToolSearch"}, {"name": "Agent"}, {"name": "mcp__fixture__read", "isMcp": true}}
	rows := []map[string]any{}
	contents := []string{
		`[{"type":"tool_result","tool_use_id":"search","content":[{"type":"tool_reference","tool_name":"mcp__fixture__read","future":true},{"type":"tool_reference","tool_name":"disconnected"},{"type":"text","text":"keep\ud800"}]}]`,
		`[{"type":"tool_result","tool_use_id":"search","content":[{"type":"tool_reference","tool_name":"disconnected"}]},{"type":"tool_result","tool_use_id":"other","content":[]}]`,
		`[{"type":"tool_result","tool_use_id":"search","content":[{"type":"tool_reference","tool_name":"Task"},{"type":"tool_reference","tool_name":""},{"type":"tool_reference","tool_name":null},{"type":"tool_reference","tool_name":0}]}]`,
		`[{"type":"tool_result","tool_use_id":"search","content":[{"type":"tool_reference","tool_name":"mcp__fixture__read"}]}]`,
	}
	for _, c := range contents {
		for _, caller := range []string{`{"type":"direct"}`, `null`} {
			for _, enabled := range []bool{false, true} {
				for _, modelEnabled := range []bool{false, true} {
					history := `[{"type":"user","message":{"role":"user","content":"original question"},"uuid":"first","timestamp":"2023-11-14T22:13:20.000Z"},{"type":"assistant","message":{"id":"response","model":"fixture-model","role":"assistant","content":[{"future":true,"type":"tool_use","id":"search","name":"ToolSearch","input":{"query":"read"},"caller":` + caller + `}],"usage":{"input_tokens":1,"output_tokens":1}},"uuid":"assistant","timestamp":"2023-11-14T22:13:20.000Z"},{"type":"user","message":{"role":"user","content":` + c + `},"uuid":"result","timestamp":"2023-11-14T22:13:20.000Z"}]`
					rows = append(rows, map[string]any{"tools": toolRows, "history": history, "enabled": enabled, "modelEnabled": modelEnabled, "model": "fixture-model"})
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-tool-search.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TOOL_SEARCH_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "normalizations": rows})
	cmd.Stdin = bytes.NewReader(data)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v %.3000s", e, output)
	}
	var source struct {
		Normalizations [][]protocol.JSONObject
		Capabilities   struct{ Blocked []string }
	}
	if e := json.Unmarshal(output, &source); e != nil || len(source.Normalizations) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %v count%d blocked%v %.1000s", e, len(source.Normalizations), source.Capabilities.Blocked, output)
	}
	definitions := []toolschema.Definition{{Kind: "registered", Name: "ToolSearch"}, {Kind: "registered", Name: "Agent"}, {Kind: "registered", Name: "mcp__fixture__read"}}
	for i, row := range rows {
		var history []protocol.JSONObject
		_ = json.Unmarshal([]byte(row["history"].(string)), &history)
		dependencies := Dependencies{UUID: func() string { return "should-not-allocate" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }}
		actual, e := normalizeHistoryForModelWithToolSearch(history, dependencies, definitions, "fixture-model", true, nil, row["enabled"].(bool))
		if e != nil {
			t.Fatal(e)
		}
		if !row["modelEnabled"].(bool) {
			actual = stripSearchFieldsAfterModelGate(actual)
		}
		project := func(records []protocol.JSONObject) []string {
			out := []string{}
			for _, record := range records {
				copy := record.Clone()
				copy.Delete("uuid")
				copy.Delete("timestamp")
				out = append(out, string(rawObject(copy)))
			}
			return out
		}
		if !reflect.DeepEqual(project(actual), project(source.Normalizations[i])) {
			t.Error(fmt.Sprintf("case%d Go=%v Source=%v", i, project(actual), project(source.Normalizations[i])))
		}
	}
}
