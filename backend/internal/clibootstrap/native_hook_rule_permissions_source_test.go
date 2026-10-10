package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNativeHookRuleOnlyPermissionsMatchActualSourceToolsAndPreserveDenyAskSafety(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	m := backgroundAgentMain(t, "http://127.0.0.1:1")
	n := m.options.Native
	if err := os.MkdirAll(n.Temp, 0700); err != nil {
		t.Fatal(err)
	}
	type row struct {
		Name       string              `json:"name"`
		Input      protocol.JSONObject `json:"input"`
		Permission protocol.JSONObject `json:"permission"`
	}
	rows := []row{}
	for _, name := range []string{"Read", "Write"} {
		for _, mode := range []string{"default", "bypassPermissions"} {
			for _, spec := range []struct{ file, key, rule string }{{"plain.txt", "", ""}, {"plain.txt", "alwaysDenyRules", name}, {"plain.txt", "alwaysAskRules", name}, {"plain.txt", "alwaysAskRules", name + "(*)"}, {"plain.txt", "alwaysDenyRules", "Read(plain.txt)"}, {"plain.txt", "alwaysDenyRules", "Edit(plain.txt)"}, {"plain.txt", "alwaysAskRules", "Read(plain.txt)"}, {"plain.txt", "alwaysAskRules", "Edit(plain.txt)"}, {".claude/settings.json", "", ""}, {".git/config", "", ""}, {".claude/launch.json", "", ""}, {".claude/skills/private/SKILL.md", "alwaysAllowRules", "Edit(/.claude/skills/private/**)"}} {
				c := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoteMain(mode)}, protocol.JSONField{Name: "alwaysDenyRules", Value: []byte(`{}`)}, protocol.JSONField{Name: "alwaysAllowRules", Value: []byte(`{}`)}, protocol.JSONField{Name: "alwaysAskRules", Value: []byte(`{}`)})
				if spec.key != "" {
					source := "session"
					c.Set(spec.key, rawMain(map[string][]string{source: {spec.rule}}))
				}
				input := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(filepath.Join(n.CWD, spec.file))})
				if name == "Write" {
					input.Set("content", quoteMain("actual permission fixture"))
				}
				rows = append(rows, row{name, input, c})
			}
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = n.Home
	cmd.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": n.Home, "hookRules": map[string]any{"cwd": n.CWD, "sessionID": m.Controller.SessionID(), "rows": rows}}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	for i, r := range rows {
		var actual json.RawMessage
		if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			if e := m.Host.SetNativePermissionMode(scope, r.Permission.String("mode")); e != nil {
				return e
			}
			updates := []protocol.JSONObject{}
			for _, pair := range []struct{ key, behavior string }{{"alwaysDenyRules", "deny"}, {"alwaysAskRules", "ask"}, {"alwaysAllowRules", "allow"}} {
				fields, _ := r.Permission.Object(pair.key)
				var list []json.RawMessage
				_ = json.Unmarshal(fields.Get("session"), &list)
				values := []protocol.JSONObject{}
				for _, raw := range list {
					parsed, e := sdkpermissions.ParsePermissionRuleValue(raw)
					if e != nil {
						return e
					}
					values = append(values, parsed)
				}
				updates = append(updates, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("replaceRules")}, protocol.JSONField{Name: "destination", Value: quoteMain("session")}, protocol.JSONField{Name: "behavior", Value: quoteMain(pair.behavior)}, protocol.JSONField{Name: "rules", Value: rawMain(values)}))
			}
			if e := m.Host.ApplyNativePermissionUpdates(scope, rawMain(updates)); e != nil {
				return e
			}
			decision, e := m.nativeTools.CheckHookRulePermissions(scope, r.Name, &r.Input)
			if e != nil {
				return e
			}
			if decision == nil {
				actual = []byte("null")
			} else {
				value := protocol.NewJSONObject(protocol.JSONField{Name: "behavior", Value: quoteMain(decision.Behavior)}, protocol.JSONField{Name: "decisionReason", Value: decision.DecisionReason}, protocol.JSONField{Name: "message", Value: decision.Message})
				if decision.Suggestions != nil {
					value.Set("suggestions", decision.Suggestions)
				}
				actual, _ = value.MarshalJSON()
			}
			return nil
		}); e != nil {
			t.Fatalf("row%d %v", i, e)
		}
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("row%d Go%s Source%s", i, actual, source.Rows[i])
		}
	}
}
