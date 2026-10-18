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

func TestTerminalMCPMenusActualSourceOptionsStatusToolsAndAuthenticationGates(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	profiles := []protocol.JSONObject{}
	for _, kind := range []string{"stdio", "http", "sse", "claudeai-proxy"} {
		for _, status := range []string{"pending", "connected", "disabled", "needs-auth", "failed"} {
			for _, tools := range []int{0, 2} {
				for _, authenticated := range []bool{false, true} {
					metadata := []protocol.JSONObject{}
					for i := 0; i < tools; i++ {
						metadata = append(metadata, protocol.NewJSONObject())
					}
					profiles = append(profiles, obj(raw(map[string]any{"config": map[string]string{"type": kind}, "status": status, "toolMetadata": metadata, "isAuthenticated": authenticated})))
				}
			}
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-menus.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_MENUS_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "menus": profiles, "errors": []any{}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual MCP menu registrations %v %.5000s", err, output)
	}
	var source struct{ Menus [][]protocol.JSONObject }
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, profile := range profiles {
		actual, _ := protocol.ParseAndStringifyJavaScriptJSON(raw(terminalMCPMenuOptions(profile)))
		want, _ := protocol.ParseAndStringifyJavaScriptJSON(raw(source.Menus[i]))
		if string(actual) != string(want) {
			t.Fatalf("Source MCP menu %d %s actual%s Source%s", i, raw(profile), actual, want)
		}
	}
}
