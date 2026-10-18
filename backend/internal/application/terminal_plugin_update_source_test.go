package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalPluginInstalledDetailsActualSourceMenusAndManagedGuards(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source required")
	}
	profiles := []protocol.JSONObject{}
	for _, scope := range []string{"user", "project", "local", "managed", "builtin", "session"} {
		for _, enabled := range []bool{true, false} {
			profiles = append(profiles, obj(raw(map[string]any{"scope": scope, "enabled": enabled, "pendingUpdate": !enabled, "hasMcpb": true, "manifest": map[string]any{"homepage": "https://example.invalid/home", "repository": "https://example.invalid/repo", "userConfig": map[string]any{"region": map[string]string{"type": "string"}}}})))
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-update.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_UPDATE_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "profiles": profiles}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Installed consumer %v %.4000s", err, output)
	}
	var wants []struct {
		Labels                []string
		Guards                map[string]*string
		ConfigurationMessages map[string]string
	}
	if err = json.Unmarshal(output, &wants); err != nil || len(wants) != len(profiles) {
		t.Fatal(err, string(output))
	}
	for i, profile := range profiles {
		t.Run(profile.String("scope")+string(profile.Get("enabled")), func(t *testing.T) {
			row := profile.Clone()
			row.Set("id", raw("sample@mkt"))
			row.Set("name", raw("sample"))
			state := &terminalLocalPanelState{pluginPanel: &terminalPluginPanelState{level: "detail", selected: row, pendingUpdates: map[string]bool{"sample@mkt": string(profile.Get("pendingUpdate")) == "true"}}}
			if err := (&Session{}).refreshTerminalPluginPanel(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			labels := []string{}
			for _, row := range state.props.Rows {
				labels = append(labels, row.Label)
			}
			if !reflect.DeepEqual(labels, wants[i].Labels) {
				t.Fatalf("actual labels %q Source %q", labels, wants[i].Labels)
			}
			for operation, expected := range wants[i].Guards {
				want := ""
				if expected != nil {
					want = *expected
				}
				if actual := terminalPluginOperationGuard(profile.String("scope"), operation); actual != want {
					t.Fatalf("%s guard %q Source %q", operation, actual, want)
				}
				if want != "" {
					// The public details action must reject before a process or
					// settings writer is available, just like the Source caller.
					err := (&Session{}).performTerminalPluginOperation(context.Background(), state, operation)
					if err == nil || err.Error() != want {
						t.Fatalf("%s consumer guard %v Source %q", operation, err, want)
					}
				}
			}
			for outcome, expected := range wants[i].ConfigurationMessages {
				if actual := terminalPluginPostEnableMessage("sample", outcome, "owned save failure"); actual != expected {
					t.Fatalf("%s post-enable output %q Source %q", outcome, actual, expected)
				}
			}
		})
	}
}
