package application

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

func TestTerminalPluginErrorsOriginalFormatterGuidanceDedupScopesAndActualRowActions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	types := []string{"path-not-found", "git-auth-failed", "git-timeout", "network-error", "manifest-parse-error", "manifest-validation-error", "plugin-not-found", "marketplace-not-found", "marketplace-load-failed", "mcp-config-invalid", "mcp-server-suppressed-duplicate", "hook-load-failed", "component-load-failed", "mcpb-download-failed", "mcpb-extract-failed", "mcpb-invalid-manifest", "marketplace-blocked-by-policy", "dependency-unsatisfied", "lsp-config-invalid", "lsp-server-start-failed", "lsp-server-crashed", "lsp-request-timeout", "lsp-request-failed", "plugin-cache-miss", "generic-error"}
	errors := []protocol.JSONObject{}
	for _, kind := range types {
		e := obj([]byte(`{"source":"sample@market","plugin":"sample","component":"skills","path":"./missing","authType":"ssh","gitUrl":"git@private.invalid:source","operation":"clone","url":"https://private.invalid","details":"offline","manifestPath":"./plugin.json","parseError":"bad JSON","validationErrors":["first","second"],"marketplace":"market","availableMarketplaces":["one","two"],"serverName":"peer","validationError":"invalid","duplicateOf":"plugin:other:peer","hookPath":"./hooks.json","reason":"not-enabled","mcpbPath":"./bundle.mcpb","allowedSources":["allowed"],"dependency":"dep@market","exitCode":null,"method":"initialize","timeoutMs":3000,"error":"actual failure","installPath":"./cache"}`))
		e.Set("type", raw(kind))
		if kind == "plugin-not-found" {
			e.Set("pluginId", raw("sample@market"))
		}
		errors = append(errors, e)
	}
	snapshots := []protocol.JSONObject{}
	for _, settings := range []string{`{}`, `{"user":{"extraKnownMarketplaces":{"market":{}}},"project":{"extraKnownMarketplaces":{"market":{}}}}`, `{"managed":{"extraKnownMarketplaces":{"market":{}}}}`, `{"managed":{"extraKnownMarketplaces":{"market":{}}},"local":{"extraKnownMarketplaces":{"market":{}}}}`} {
		snapshots = append(snapshots, protocol.NewJSONObject(protocol.JSONField{Name: "errors", Value: raw(errors)}, protocol.JSONField{Name: "errorSettings", Value: []byte(settings)}, protocol.JSONField{Name: "pluginScopes", Value: raw(map[string]string{"sample@market": "local"})}, protocol.JSONField{Name: "brokenMarketplaces", Value: raw([]map[string]string{{"name": "market", "error": "duplicated-known"}, {"name": "broken", "error": "physical-read-error"}})}, protocol.JSONField{Name: "failedMarketplaces", Value: raw([]map[string]string{{"name": "failed"}})}))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-errors.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_ERRORS_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "snapshots": snapshots}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source Errors %v %.4000s", err, output)
	}
	var wants []struct {
		Rows    []protocol.JSONObject
		Display []protocol.JSONObject
	}
	if err = json.Unmarshal(output, &wants); err != nil {
		t.Fatal(err, string(output))
	}
	for i, snapshot := range snapshots {
		actual := terminalPluginErrorRows(snapshot)
		var a, b any
		_ = json.Unmarshal(raw(actual), &a)
		_ = json.Unmarshal(raw(wants[i].Rows), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("actual error rows profile%d %s Source%s", i, raw(actual), raw(wants[i].Rows))
		}
		for j, e := range errors {
			got := protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: raw(terminalPluginErrorMessage(e))}, protocol.JSONField{Name: "guidance", Value: terminalPluginErrorGuidance(e)})
			_ = json.Unmarshal(raw(got), &a)
			_ = json.Unmarshal(raw(wants[i].Display[j]), &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("actual Errors display %s %s Source%s", e.String("type"), raw(got), raw(wants[i].Display[j]))
			}
		}
	}
}
