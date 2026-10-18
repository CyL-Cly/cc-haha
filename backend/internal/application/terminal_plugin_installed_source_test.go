package application

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalPluginInstalledActualSourceUnifiedScopesChildrenFailedFlaggedAndCollation(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned original Source required")
	}
	profiles := []protocol.JSONObject{}
	for _, scope := range []string{"user", "local", "project", "managed", "builtin", "dynamic"} {
		plugins := []protocol.JSONObject{obj(raw(map[string]any{"id": "zeta@mkt", "name": "zeta", "scope": scope, "manifest": map[string]any{"name": "zeta"}, "enabled": true})), obj(raw(map[string]any{"id": "alpha@other", "name": "alpha", "scope": "user", "manifest": map[string]any{"name": "alpha"}, "enabled": false}))}
		clients := []protocol.JSONObject{}
		for _, client := range []struct{ name, scope, status string }{{"plugin:zeta:one:two", "dynamic", "connected"}, {"plugin:zeta:other", "dynamic", "disabled"}, {"plugin:missing:orphan", "dynamic", "connected"}, {"ide", "dynamic", "connected"}, {"Écho", "user", "connected"}, {"echo", "user", "pending"}, {"É", "project", "needs-auth"}, {"Ω", "local", "failed"}} {
			clients = append(clients, obj(raw(map[string]any{"name": client.name, "status": client.status, "scope": client.scope, "config": map[string]string{"scope": client.scope}})))
		}
		snapshot := obj(raw(map[string]any{"manageInstalled": plugins, "installed": plugins, "flaggedPlugins": map[string]any{"flagged@mkt": map[string]string{"flaggedAt": "2026-10-14T00:00:00Z"}}, "pluginScopes": map[string]string{"failed@mkt": "project", "other@none": "flag"}, "errors": []any{map[string]string{"type": "generic-error", "source": "failed@mkt", "error": "first"}, map[string]string{"type": "generic-error", "source": "other@none", "error": "second"}, map[string]string{"type": "generic-error", "source": "flagged@mkt", "error": "flagged has no duplicate failed row"}, map[string]string{"type": "generic-error", "source": "zeta@mkt", "error": "matched plugin"}}}))
		profiles = append(profiles, obj(raw(map[string]any{"snapshot": snapshot, "clients": clients, "settings": map[string]any{"enabledPlugins": map[string]bool{"zeta@mkt": true, "alpha@other": false}}})))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-installed.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_INSTALLED_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "profiles": profiles}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source unified Installed %v %.5000s", err, output)
	}
	var original [][]protocol.JSONObject
	if err = json.Unmarshal(output, &original); err != nil {
		t.Fatal(err, string(output))
	}
	for i, profile := range profiles {
		rows, err := terminalPluginUnifiedInstalled(obj(profile.Get("snapshot")), array(profile.Get("clients")))
		if err != nil {
			t.Fatal(err)
		}
		actual := []protocol.JSONObject{}
		for _, row := range rows {
			status := row.Get("status")
			if status == nil {
				status = []byte("null")
			}
			actual = append(actual, protocol.NewJSONObject(protocol.JSONField{Name: "itemType", Value: row.Get("itemType")}, protocol.JSONField{Name: "id", Value: row.Get("id")}, protocol.JSONField{Name: "name", Value: row.Get("name")}, protocol.JSONField{Name: "scope", Value: row.Get("scope")}, protocol.JSONField{Name: "status", Value: status}, protocol.JSONField{Name: "indented", Value: raw(string(row.Get("indented")) == "true")}))
		}
		got, _ := protocol.ParseAndStringifyJavaScriptJSON(raw(actual))
		want, _ := protocol.ParseAndStringifyJavaScriptJSON(raw(original[i]))
		if string(got) != string(want) {
			t.Fatalf("Source unified scope%d got%s Source%s", i, got, want)
		}
	}
}
