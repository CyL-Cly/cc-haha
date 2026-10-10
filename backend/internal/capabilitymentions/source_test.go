package capabilitymentions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func parsed(text string) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON([]byte(text))
	return value
}

func TestCallableIdentityProjectionMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	compare := collate.New(language.English)
	base := parsed(`{"type":"prompt","name":"literal\"&<😀","description":"Do not install automatically","source":"userSettings","loadedFrom":"skills","userInvocable":true}`)
	rows := []Snapshot{{Commands: []protocol.JSONObject{base}}, {}}
	for _, change := range []string{`{"type":"local"}`, `{"disableModelInvocation":true}`, `{"disableNonInteractive":true}`, `{"userInvocable":false}`, `{"isHidden":true}`, `{"isEnabled":false}`, `{"source":"builtin"}`, `{"loadedFrom":"other"}`, `{"loadedFrom":"other","hasUserSpecifiedDescription":true}`, `{"loadedFrom":"other","whenToUse":"actual"}`, `{"userFacingName":"display"}`, `{"userFacingName":""}`, `{"skillRoot":"/owned path","description":"untrusted injection"}`, `{"name":""}`, `{"description":null}`, `{"description":0}`, `{"name":"raw\ud800"}`} {
		command := base.Clone()
		command.Assign(parsed(change))
		rows = append(rows, Snapshot{Commands: []protocol.JSONObject{command}})
	}
	plugin := parsed(`{"name":"native","source":"native@mkt","enabled":true,"path":"/owned/plugin","manifest":{"description":"actual plugin"},"mcpServers":{"2":{},"1":{},"service":{}}}`)
	command := parsed(`{"type":"prompt","name":"native:work","description":"exact work","source":"plugin","loadedFrom":"skills","pluginInfo":{"repository":"native@mkt"}}`)
	rows = append(rows, Snapshot{Commands: []protocol.JSONObject{command}, EnabledPlugins: []protocol.JSONObject{plugin}}, Snapshot{Commands: []protocol.JSONObject{command}, EnabledPlugins: []protocol.JSONObject{plugin}, FailedPluginIDs: []string{"native@mkt"}}, Snapshot{EnabledPlugins: []protocol.JSONObject{plugin}}, Snapshot{EnabledPlugins: []protocol.JSONObject{parsed(`{"name":"hooks-only","source":"hooks@mkt","enabled":true,"manifest":{},"path":"/owned/hooks"}`)}})
	rows = append(rows, Snapshot{Commands: []protocol.JSONObject{command, command, base}, EnabledPlugins: []protocol.JSONObject{plugin}, Brands: []protocol.JSONObject{parsed(`{"pluginId":"native@mkt","id":"office-native","displayName":"Branded Native","description":"brand"}`)}})
	rows = append(rows, Snapshot{Commands: []protocol.JSONObject{command}, EnabledPlugins: []protocol.JSONObject{plugin}, Brands: []protocol.JSONObject{parsed(`{"pluginId":"native@mkt","id":"Invalid/brand"}`)}})
	input := []map[string]any{}
	for _, row := range rows {
		input = append(input, map[string]any{"commands": append([]protocol.JSONObject{}, row.Commands...), "enabledPlugins": append([]protocol.JSONObject{}, row.EnabledPlugins...), "failedPluginIds": row.FailedPluginIDs, "brands": row.Brands})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-capability-mentions.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_NATIVE_CAPABILITY_MENTIONS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	body, _ := json.Marshal(map[string]any{"snapshots": input})
	cmd.Stdin = bytes.NewReader(body)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source %v %.4000s", err, output)
	}
	var source struct {
		Results      []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("invalid Source %v %.2500s", err, output)
	}
	for i, row := range rows {
		row.Compare = compare.CompareString
		data, _ := json.Marshal(Build(row))
		got, _ := protocol.ParseAndStringifyJavaScriptJSON(data)
		want, _ := protocol.ParseAndStringifyJavaScriptJSON(source.Results[i])
		if !bytes.Equal(got, want) {
			t.Errorf("profile%d Go=%s Source=%s", i, got, want)
		}
	}
}
