package clibootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalPluginErrorMergeActualSourcePreservesIndependentLSPAndRawUTF16Keys(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	previous := []protocol.JSONObject{}
	for _, encoded := range []string{`{"type":"generic-error","source":"plugin:a","error":"\ud800"}`, `{"type":"generic-error","source":"plugin:a","error":"\ud801"}`, `{"type":"lsp-config-invalid","source":"lsp-manager","validationError":"old"}`, `{"type":"generic-error","source":"old-market","error":"retired loading failure"}`, `{"type":"generic-error","source":"plugin:b","error":"keep"}`} {
		previous = append(previous, mainObj([]byte(encoded)))
	}
	profiles := []protocol.JSONObject{}
	for _, incoming := range [][]protocol.JSONObject{nil, {mainObj([]byte(`{"type":"generic-error","source":"plugin:a","error":"\ud800"}`))}, {mainObj([]byte(`{"type":"lsp-config-invalid","source":"lsp-manager","validationError":"new"}`))}} {
		profiles = append(profiles, mainObj(rawMain(map[string]any{"previous": previous, "incoming": append([]protocol.JSONObject{}, incoming...)})))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-menus.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_MENUS_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "menus": []any{}, "errors": profiles}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual useManagePlugins merge %v %.5000s", err, output)
	}
	var source struct{ Errors [][]protocol.JSONObject }
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, profile := range profiles {
		var incoming []protocol.JSONObject
		_ = json.Unmarshal(profile.Get("incoming"), &incoming)
		actual, _ := protocol.ParseAndStringifyJavaScriptJSON(rawMain(terminalPluginMergedErrors(previous, incoming)))
		want, _ := protocol.ParseAndStringifyJavaScriptJSON(rawMain(source.Errors[i]))
		if string(actual) != string(want) {
			t.Fatalf("Source error merge %d actual%s Source%s", i, actual, want)
		}
	}
}
