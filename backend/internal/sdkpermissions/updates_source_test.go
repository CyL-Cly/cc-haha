package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestPermissionUpdateBatchesAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun unavailable")
	}
	type item struct {
		Context map[string]any   `json:"context"`
		Updates []map[string]any `json:"updates"`
	}
	initial := func() map[string]any {
		return map[string]any{"mode": "default", "isBypassPermissionsModeAvailable": true, "alwaysAllowRules": map[string]any{"session": []string{"Read", "Bash(*)"}, "cliArg": []string{"Write"}}, "alwaysDenyRules": map[string]any{}, "alwaysAskRules": map[string]any{}, "unknown": map[string]any{"keep": true}}
	}
	rows := []item{}
	for _, destination := range []string{"session", "cliArg"} {
		for _, kind := range []string{"addRules", "replaceRules", "removeRules"} {
			for _, behavior := range []string{"allow", "deny", "ask"} {
				rows = append(rows, item{initial(), []map[string]any{{"type": kind, "destination": destination, "behavior": behavior, "rules": []any{map[string]any{"toolName": "Bash", "ruleContent": "echo (x)\\n"}, map[string]any{"toolName": "Read"}, map[string]any{"toolName": "Task", "ruleContent": ""}}}}})
			}
		}
	}
	for _, mode := range []string{"default", "plan", "acceptEdits", "bypassPermissions"} {
		for _, available := range []bool{false, true} {
			c := initial()
			c["isBypassPermissionsModeAvailable"] = available
			rows = append(rows, item{c, []map[string]any{{"type": "setMode", "mode": mode, "destination": "session"}}})
		}
	}
	rows = append(rows, item{initial(), []map[string]any{{"type": "addDirectories", "directories": []string{"/fixture/a", "/fixture/b", "/fixture/a"}, "destination": "session"}, {"type": "addDirectories", "directories": []string{"/fixture/a"}, "destination": "cliArg"}, {"type": "removeDirectories", "directories": []string{"/fixture/b"}, "destination": "session"}}})
	rows = append(rows, item{initial(), []map[string]any{{"type": "addRules", "destination": "session", "behavior": "allow", "rules": []any{map[string]any{"toolName": "Read"}, map[string]any{"toolName": "Read"}}}, {"type": "removeRules", "destination": "session", "behavior": "allow", "rules": []any{map[string]any{"toolName": "Read"}}}}})
	inputs := []map[string]any{}
	for _, row := range rows {
		inputs = append(inputs, map[string]any{"context": row.Context, "updates": row.Updates})
	}
	schemas := []string{`{"type":"addRules","destination":"session","behavior":"allow","rules":[{"toolName":"Read","unknown":true}],"unknown":true}`, `{"type":"replaceRules","destination":"cliArg","behavior":"deny","rules":[]}`, `{"type":"removeRules","destination":"session","behavior":"ask","rules":[{"toolName":"Bash","ruleContent":"\ud800\\(\udfff)"}]}`, `{"type":"setMode","destination":"session","mode":"acceptEdits","unknown":3}`, `{"type":"addDirectories","destination":"session","directories":["","\ud800"]}`, `{"type":"removeDirectories","destination":"cliArg","directories":[]}`, `{"type":"addRules","destination":"session","behavior":"allow","rules":null}`, `{"type":"setMode","destination":"session","mode":"unexpected"}`, `{"type":"addRules","destination":"session","behavior":"allow","rules":[{"toolName":4}]}`, `{"type":"addDirectories","destination":"session","directories":[null]}`}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plan.fixture.ts")
	run := func(input any) protocol.JSONObject {
		t.Helper()
		data, _ := json.Marshal(input)
		cmd := exec.Command(bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_NATIVE_PLAN_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
		cmd.Stdin = bytes.NewReader(data)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("Source %v %.2000s", e, out)
		}
		var value protocol.JSONObject
		if e := value.UnmarshalJSON(out); e != nil {
			t.Fatal(e, string(out))
		}
		return value
	}
	source := run(map[string]any{"homeDir": home, "permissionUpdates": inputs, "inputs": []any{}})
	var observed []protocol.JSONObject
	if e := json.Unmarshal(source.Get("observations"), &observed); e != nil {
		t.Fatal(e)
	}
	if len(observed) != len(rows) {
		t.Fatal("Source observations missing", source)
	}
	owner := cooperative.New()
	defer owner.Close()
	for i, row := range rows {
		if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			data, _ := json.Marshal(row.Context)
			var ctx protocol.JSONObject
			_ = ctx.UnmarshalJSON(data)
			ctx.Set("additionalWorkingDirectories", []byte(`{"map":[]}`))
			s := &State{owner: owner, nativeProcess: true, context: ctx}
			batch, _ := json.Marshal(row.Updates)
			updates := ParsePermissionUpdates(batch)
			if len(updates) != len(row.Updates) {
				t.Fatal("valid batch rejected", i)
			}
			if e := s.ApplyPermissionUpdates(scope, updates, func(mode string) error { return s.SetMode(scope, mode) }, nil); e != nil {
				return e
			}
			got, _ := s.PlanModeContext(scope)
			var expected protocol.JSONObject
			_ = expected.UnmarshalJSON(observed[i].Get("context"))
			for _, field := range []string{"mode", "prePlanMode", "alwaysAllowRules", "alwaysDenyRules", "alwaysAskRules", "unknown"} {
				a, _ := protocol.ParseAndStringifyJavaScriptJSON(got.Get(field))
				b, _ := protocol.ParseAndStringifyJavaScriptJSON(expected.Get(field))
				if string(a) != string(b) {
					t.Fatalf("row %d %s Source=%s Go=%s", i, field, b, a)
				}
			}
			var dirs protocol.JSONObject
			_ = dirs.UnmarshalJSON(got.Get("additionalWorkingDirectories"))
			a, _ := protocol.ParseAndStringifyJavaScriptJSON(dirs.Get("map"))
			b, _ := protocol.ParseAndStringifyJavaScriptJSON(expected.Get("additionalWorkingDirectories"))
			if string(a) != string(b) {
				t.Fatalf("row %d directories Source=%s Go=%s", i, b, a)
			}
			return nil
		}); e != nil {
			t.Fatal(i, e)
		}
	}
	schemaResult := run(map[string]any{"homeDir": home, "updateSchemas": schemas, "inputs": []any{}})
	var cases []struct {
		SDK struct {
			Success bool            `json:"success"`
			Data    json.RawMessage `json:"data"`
		} `json:"sdk"`
	}
	if e := json.Unmarshal(schemaResult.Get("schemas"), &cases); e != nil {
		t.Fatal(e)
	}
	for i, row := range schemas {
		got := ParsePermissionUpdates([]byte("[" + row + "]"))
		if len(got) == 1 != cases[i].SDK.Success {
			t.Fatalf("schema %d acceptance Source=%v Go=%d", i, cases[i].SDK.Success, len(got))
		}
		if len(got) == 1 {
			a, _ := got[0].MarshalJSON()
			a, _ = protocol.ParseAndStringifyJavaScriptJSON(a)
			b, _ := protocol.ParseAndStringifyJavaScriptJSON(cases[i].SDK.Data)
			if string(a) != string(b) {
				t.Fatalf("schema %d Source=%s Go=%s", i, b, a)
			}
		}
	}
	if ParsePermissionUpdates([]byte(`[ {"type":"setMode","mode":"acceptEdits","destination":"session"},{}]`)) != nil {
		t.Fatal("malformed batch partially applied")
	}
}
