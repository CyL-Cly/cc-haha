package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPluginMCPBLegacyRawDetectorOriginalConfigureGuardAndActualMenu(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("pinned Source + verified compatible MCPB reference required")
	}
	for _, text := range []string{`{"plugins":[{"name":"sample","mcpServers":["./plain.json","./legacy.dxt"]}]}`, `{"plugins":[{"name":"other","mcpServers":"./bundle.mcpb"}]}`, `{"plugins":[{"name":"sample","mcpServers":"./bundle.MCPB"}]}`, `not-json`} {
		t.Run(text, func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			plugin := obj(raw(map[string]any{"name": "sample", "path": filepath.Join(home, "plugin"), "manifest": map[string]any{"name": "sample"}}))
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-mcpb-manage.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_MCPB_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "plugin": plugin, "steps": []map[string]string{{"action": "detect"}, {"action": "open"}}, "files": map[string]string{filepath.Join(home, ".claude/settings.json"): base64.StdEncoding.EncodeToString([]byte(`{}`)), filepath.Join(home, ".claude-plugin/marketplace.json"): base64.StdEncoding.EncodeToString([]byte(text))}}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual legacy detector %v %.5000s", err, output)
			}
			var original []protocol.JSONObject
			if err = json.Unmarshal(output, &original); err != nil {
				t.Fatal(err, string(output))
			}
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			prepareTerminalPluginFixture(t, o)
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if _, err = s.process.TerminalPluginOperation(context.Background(), plugincli.Command{Action: "install", Argument: "sample@mkt", Scope: "user"}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.process.TerminalPluginSnapshot(context.Background(), false)
			if err != nil {
				t.Fatal(err)
			}
			installed := array(snapshot.Get("installed"))
			if len(installed) != 1 {
				t.Fatal(raw(snapshot))
			}
			path := filepath.Join(installed[0].String("installPath"), "..", ".claude-plugin/marketplace.json")
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			if err = os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			var panel *tui.LocalPanel
			emit := func(event tui.Event) error {
				if event.LocalPanel != nil {
					copy := *event.LocalPanel
					panel = &copy
				}
				return nil
			}
			if _, err = s.InvokeTerminalCommand(context.Background(), "plugin", "manage", emit); err != nil {
				t.Fatal(err)
			}
			if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "plugin-open", Row: panel.Rows[0].ID}, emit); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range panel.Rows {
				if row.ID == "mcpb" {
					found = true
				}
			}
			if found != (string(original[0].Get("hasMcpb")) == "true") {
				t.Fatalf("actual raw detector/menu %t Source%s", found, raw(original))
			}
			_, err = s.process.TerminalPluginMCPBConfiguration(context.Background(), "sample@mkt", nil)
			if err == nil || err.Error() != original[1].String("error") || len(array(original[1].Get("calls"))) != 0 {
				t.Fatal("actual Configure requires real manifest before bundle I/O", err, raw(original))
			}
		})
	}
}
