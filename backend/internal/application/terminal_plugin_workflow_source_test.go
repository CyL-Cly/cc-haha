package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func terminalPluginInitialView(args string) map[string]any {
	p := parseTerminalPluginArgs(args)
	view := map[string]any{"type": "discover-plugins"}
	switch p.level {
	case "help":
		return map[string]any{"type": "help"}
	case "validate":
		view["type"] = "validate"
		if p.target != "" {
			view["path"] = p.target
		}
		return view
	case "add-marketplace":
		return map[string]any{"type": "add-marketplace", "initialValue": p.target}
	}
	if p.tab == "installed" {
		view["type"] = "manage-plugins"
		if p.target != "" {
			view["targetPlugin"] = p.target
		}
		if p.action != "" {
			view["action"] = p.action
		}
		return view
	}
	if p.tab == "marketplaces" {
		if p.action == "list" {
			return map[string]any{"type": "marketplace-list"}
		}
		if p.action == "remove" || p.action == "update" {
			return map[string]any{"type": "manage-marketplaces", "targetMarketplace": p.target, "action": p.action}
		}
		return map[string]any{"type": "marketplace-menu"}
	}
	if p.marketplace != "" {
		return map[string]any{"type": "browse-marketplace", "targetMarketplace": p.marketplace}
	}
	if strings.Contains(p.target, "@") {
		parts := strings.Split(p.target, "@")
		return map[string]any{"type": "browse-marketplace", "targetMarketplace": parts[1], "targetPlugin": parts[0]}
	}
	if p.target != "" {
		view["targetPlugin"] = p.target
	}
	return view
}
func TestTerminalPluginAndWorkflowConsumersMatchActualOriginalFunctions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	args := []string{"", " HELP ", "install", "i sample", "install sample@mkt@ignored", "install https://owned.example/mkt", "install ./market", "manage", "enable sample@mkt", "disable", "uninstall sample", "validate ./path with spaces", "marketplace add ./market with spaces", "market rm mkt", "marketplace update", "marketplace list", "unknown", "marketplace unknown"}
	progress := [][]protocol.JSONObject{array([]byte(`[]`)), array([]byte(`[{"type":"workflow_agent","index":1,"label":"orphan","state":"start"},{"type":"workflow_agent","index":2,"phaseIndex":9,"phaseTitle":"Synthetic","state":"done"},{"type":"workflow_phase","index":9,"title":"Later ignored"},{"type":"workflow_phase","index":2,"title":"Earlier"},{"type":"workflow_agent","index":3,"phaseIndex":2,"state":"error"},{"type":"workflow_log","message":"Actual log"}]`)), array([]byte(`[{"type":"workflow_agent","index":1,"phaseIndex":0,"state":"progress"},{"type":"workflow_agent","index":2,"state":"done"},{"type":"workflow_phase","index":4,"title":"Empty"},{"type":"workflow_phase","index":4,"title":"Duplicate ignored"}]`))}
	agents := array([]byte(`[{"type":"workflow_agent","index":1,"state":"start"},{"type":"workflow_agent","index":2,"state":"progress"},{"type":"workflow_agent","index":3,"state":"done"},{"type":"workflow_agent","index":4,"state":"error"}]`))
	filters := []map[string]any{}
	for _, filter := range []string{"all", "running", "done", "error"} {
		filters = append(filters, map[string]any{"agents": agents, "filter": filter})
	}
	values := []map[string]any{{"schema": map[string]any{"secret": map[string]any{"type": "string", "sensitive": true}, "n": map[string]any{"type": "number"}, "flag": map[string]any{"type": "boolean"}}, "initial": map[string]any{"secret": "private"}, "collected": map[string]string{"secret": "", "n": " ", "flag": "YES"}}, {"schema": map[string]any{"n": map[string]any{"type": "number"}, "text": map[string]any{"type": "string"}}, "initial": map[string]any{}, "collected": map[string]string{"n": "invalid", "text": "actual"}}}
	for _, text := range []string{"0x10", "0Xffffffffffffffffffff", "0o17", "0b11", "0x1p2", "Inf", "Infinity", "+Infinity", "-Infinity", "1e400", "1_000", "+.5", "-0", "\ufeff17\ufeff", "\u008517\u0085", "\ufeff"} {
		values = append(values, map[string]any{"schema": map[string]any{"n": map[string]any{"type": "number"}}, "initial": map[string]any{}, "collected": map[string]string{"n": text}})
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-workflow.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_WORKFLOW_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "pluginArgs": args, "progress": progress, "filters": filters, "values": values}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Source %v %.4000s", err, output)
	}
	var source struct{ PluginViews, WorkflowGroups, WorkflowFilters, FinalValues []json.RawMessage }
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	compare := func(label string, actual any, want json.RawMessage) {
		t.Helper()
		var got, expected any
		_ = json.Unmarshal(raw(actual), &got)
		_ = json.Unmarshal(want, &expected)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s Go=%s Source=%s", label, raw(actual), want)
		}
	}
	for i, arg := range args {
		compare("plugin "+arg, terminalPluginInitialView(arg), source.PluginViews[i])
	}
	for i, events := range progress {
		phases, orphans, logs := terminalWorkflowGroups(events)
		compare("groups", map[string]any{"phases": phases, "orphanAgents": orphans, "logs": logs}, source.WorkflowGroups[i])
	}
	for i, row := range filters {
		compare("filter", terminalWorkflowFilter(agents, row["filter"].(string)), source.WorkflowFilters[i])
	}
	for i, row := range values {
		schema := obj(raw(row["schema"]))
		present := protocol.NewJSONObject()
		initial := obj(raw(row["initial"]))
		for _, property := range schema.OwnProperties() {
			var key string
			_ = json.Unmarshal(property.NameJSON, &key)
			if string(obj(property.Value).Get("sensitive")) == "true" && initial.Get(key) != nil {
				present.Set(key, []byte("true"))
			}
		}
		step := protocol.NewJSONObject(protocol.JSONField{Name: "schema", Value: raw(schema)}, protocol.JSONField{Name: "savedSensitive", Value: raw(present)})
		compare("option values", terminalPluginFinalValues(step, row["collected"].(map[string]string)), source.FinalValues[i])
	}
}
