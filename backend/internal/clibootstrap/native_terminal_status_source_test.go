package clibootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeStatusPolicyOriginAndMCPCountsAgainstActualSourceFunctions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	policies := []map[string]any{{}, {"admin": map[string]any{"language": "Deutsch"}}, {"admin": map[string]any{"language": "Deutsch"}, "platform": "windows"}, {"base": map[string]any{"language": "Deutsch"}}, {"drop": map[string]any{"01.json": map[string]any{"language": "Deutsch"}}}, {"base": map[string]any{"language": "Deutsch"}, "drop": map[string]any{"01.json": map[string]any{}}}, {"user": map[string]any{"language": "Deutsch"}, "platform": "windows"}, {"remote": map[string]any{"language": "Deutsch"}, "admin": map[string]any{"language": "Spanish"}, "base": map[string]any{"language": "Chinese"}}}
	clients := [][]map[string]any{{}, {{"name": "ide", "type": "connected"}}, {{"name": "one", "type": "connected"}, {"name": "two", "type": "pending"}, {"name": "three", "type": "needs-auth"}, {"name": "four", "type": "disabled"}, {"name": "five", "type": "failed"}, {"name": "ide", "type": "pending"}}}
	ide := []map[string]any{{"clients": []any{}, "installation": nil}, {"clients": []any{}, "installation": map[string]any{"installed": true, "ideType": "vscode", "installedVersion": "2"}}, {"clients": []any{}, "installation": map[string]any{"installed": false, "ideType": "intellij", "error": "physical install denied"}}}
	for _, status := range []string{"connected", "failed"} {
		for _, installation := range []any{nil, map[string]any{"installed": true, "ideType": "vscode", "installedVersion": "1"}, map[string]any{"installed": true, "ideType": "intellij", "installedVersion": "2"}, map[string]any{"installed": false, "ideType": "vscode"}} {
			ide = append(ide, map[string]any{"clients": []any{map[string]any{"name": "ide", "type": status, "ideName": "VS Code", "serverInfo": map[string]any{"version": "1"}}}, "installation": installation})
		}
	}
	models := []map[string]any{}
	for _, kind := range []string{"api", "pro", "max", "team"} {
		for _, disabled := range []bool{false, true} {
			env := map[string]string{}
			if disabled {
				env["CLAUDE_CODE_DISABLE_1M_CONTEXT"] = "1"
			}
			subscriber := kind != "api"
			tier := ""
			if kind == "team" {
				tier = "default_claude_max_5x"
			}
			selector, e := modelselection.NewAnthropicAuthentication(modelselection.Options{Environment: env, Settings: protocol.NewJSONObject()}, modelselection.MetadataIdentity{Subscriber: subscriber, SubscriptionType: kind, RateLimitTier: tier})
			if e != nil {
				t.Fatal(e)
			}
			resolved, e := nativePickerResolve(selector, nil)
			if e != nil {
				t.Fatal(e)
			}
			models = append(models, map[string]any{"env": env, "subscriber": subscriber, "subscription": kind, "tier": tier, "resolved": resolved, "model": nil, "merge": !disabled})
		}
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-status.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_STATUS_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "policies": policies, "clients": clients, "ide": ide, "models": models}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Source %v %.4000s", err, output)
	}
	var source struct {
		Policy []struct {
			Origin              *string
			HasBase, HasDropIns bool
		}
		MCP   [][]TerminalStatusProperty
		IDE   [][]TerminalStatusProperty
		Model []string
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for index, row := range policies {
		root := t.TempDir()
		config, managed := filepath.Join(root, ".claude"), filepath.Join(root, "managed")
		_ = os.MkdirAll(config, 0700)
		_ = os.MkdirAll(managed, 0700)
		if value, ok := row["base"]; ok {
			_ = os.WriteFile(filepath.Join(managed, "managed-settings.json"), rawMain(value), 0600)
		}
		if drop, ok := row["drop"].(map[string]any); ok {
			_ = os.MkdirAll(filepath.Join(managed, "managed-settings.d"), 0700)
			for name, value := range drop {
				_ = os.WriteFile(filepath.Join(managed, "managed-settings.d", name), rawMain(value), 0600)
			}
		}
		env := map[string]string{"HOME": root, "CLAUDE_CONFIG_DIR": config, "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}
		if value, ok := row["remote"]; ok {
			env["ANTHROPIC_BASE_URL"] = "https://api.anthropic.com"
			env["ANTHROPIC_API_KEY"] = "fake-never-network"
			_ = os.WriteFile(filepath.Join(config, "remote-settings.json"), rawMain(value), 0600)
		}
		object := func(key string) *protocol.JSONObject {
			value, ok := row[key]
			if !ok {
				return nil
			}
			o := mainObj(rawMain(value))
			return &o
		}
		n, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: root, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) {
			return object("admin"), object("user"), nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if row["platform"] == "windows" {
			n.platform = "windows"
		}
		_, err = n.ResolvePolicy()
		if err != nil {
			t.Fatal(err)
		}
		observed := n.PolicyObservation()
		expected := ""
		if source.Policy[index].Origin != nil {
			expected = *source.Policy[index].Origin
		}
		if observed.Origin != expected || observed.HasBase != source.Policy[index].HasBase && observed.Origin == "file" || observed.HasDropIns != source.Policy[index].HasDropIns && observed.Origin == "file" {
			t.Fatal("actual selected policy branch differs Source", index, observed, source.Policy[index])
		}
	}
	for index, rows := range clients {
		native := []protocol.JSONObject{}
		for _, row := range rows {
			native = append(native, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: rawMain(row["name"])}, protocol.JSONField{Name: "status", Value: rawMain(row["type"])}))
		}
		property := terminalStatusMCPProperty(native)
		got := []TerminalStatusProperty{}
		if property != nil {
			got = append(got, *property)
		}
		var a, b any
		_ = json.Unmarshal(rawMain(got), &a)
		_ = json.Unmarshal(rawMain(source.MCP[index]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal(index, string(rawMain(got)), string(rawMain(source.MCP[index])))
		}
	}
	for index, row := range ide {
		sourceClients := row["clients"].([]any)
		native := []protocol.JSONObject{}
		for _, raw := range sourceClients {
			record := raw.(map[string]any)
			copy := mainObj(rawMain(record))
			copy.Set("status", copy.Get("type"))
			native = append(native, copy)
		}
		var installation *ideintegration.ExtensionStatus
		if row["installation"] != nil {
			installation = &ideintegration.ExtensionStatus{}
			_ = json.Unmarshal(rawMain(row["installation"]), installation)
		}
		actual := []TerminalStatusProperty{}
		if value := terminalStatusIDEProperty(native, installation); value != nil {
			actual = append(actual, *value)
		}
		var a, b any
		_ = json.Unmarshal(rawMain(actual), &a)
		_ = json.Unmarshal(rawMain(source.IDE[index]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("IDE", index, string(rawMain(actual)), string(rawMain(source.IDE[index])))
		}
	}
	for index, row := range models {
		env := row["env"].(map[string]string)
		subscriber := row["subscriber"].(bool)
		kind, tier := row["subscription"].(string), row["tier"].(string)
		selector, e := modelselection.NewAnthropicAuthentication(modelselection.Options{Environment: env, Settings: protocol.NewJSONObject()}, modelselection.MetadataIdentity{Subscriber: subscriber, SubscriptionType: kind, RateLimitTier: tier})
		if e != nil {
			t.Fatal(e)
		}
		tokens := openaiauth.ClaudeEnvironmentToken("private-never-requested")
		tokens.Object.Set("subscriptionType", rawMain(kind))
		tokens.Object.Set("rateLimitTier", rawMain(tier))
		actual, e := terminalStatusModelLabel(selector, nil, subscriber, tokens, env)
		if e != nil || actual != source.Model[index] {
			t.Fatal("model", index, actual, source.Model[index], e)
		}
	}

}
