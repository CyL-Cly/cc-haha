package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPluginMCPBOriginalManageCallerActualLoaderForcedOpenCancelSaveAndSecretDomains(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("verified compatible published MCPB reference +pinned Source Bun required")
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	prepareTerminalMCPBFixture(t, o)
	bundle, err := os.ReadFile(filepath.Join(o.WorkDir, "market", "plugins", "sample", "bundle.mcpb"))
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(home, "plugin")
	steps := []map[string]any{{"action": "open"}, {"action": "cancel"}, {"action": "open"}, {"action": "save", "values": map[string]string{"word": "FIRST", "token": "PRIVATE_BUNDLE"}}, {"action": "open"}, {"action": "save", "values": map[string]string{"word": "INVALID_BLANK"}}, {"action": "open"}, {"action": "save", "values": map[string]string{"word": "FINAL", "token": "PRIVATE_BUNDLE"}}}
	plugin := obj([]byte(`{"name":"sample","manifest":{"mcpServers":["./ignored.json","./bundle.mcpb","./unused.dxt"]}}`))
	plugin.Set("path", raw(pluginPath))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-mcpb-manage.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_MCPB_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "plugin": plugin, "steps": steps, "files": map[string]string{filepath.Join(pluginPath, "bundle.mcpb"): base64.StdEncoding.EncodeToString(bundle), filepath.Join(home, ".claude", "settings.json"): base64.StdEncoding.EncodeToString([]byte(`{"futureUser":{"keep":true}}`)), filepath.Join(home, ".claude", ".credentials.json"): base64.StdEncoding.EncodeToString([]byte(`{"futureSecret":"keep"}`))}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source Manage caller %v %.5000s", err, output)
	}
	var wants []protocol.JSONObject
	if err = json.Unmarshal(output, &wants); err != nil {
		t.Fatal(err, string(output))
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	_, err = s.InvokeTerminalCommand(context.Background(), "plugin", "install sample@mkt", emit)
	if err != nil {
		t.Fatal(err)
	}
	apply := func(action, row string, fields map[string]string) {
		t.Helper()
		if err := s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: action, Row: row, Fields: fields}, emit); err != nil {
			t.Fatal(err)
		}
	}
	apply("plugin-install-user", "install", nil)
	_, err = s.InvokeTerminalCommand(context.Background(), "plugin", "manage", emit)
	if err != nil {
		t.Fatal(err)
	}
	apply("plugin-open", panel.Rows[0].ID, nil)
	for i, step := range steps {
		switch step["action"] {
		case "open":
			apply("plugin-configure-mcpb", "mcpb", nil)
		case "cancel":
			apply("plugin-back", "", nil)
		case "save":
			apply("save", "", step["values"].(map[string]string))
		}
		want := wants[i]
		if want.String("error") != "" {
			t.Fatal("unexpected Source error", raw(want))
		}
		if want.Get("needed") != nil && string(want.Get("needed")) != "null" {
			expected := obj(want.Get("needed"))
			schema := obj(expected.Get("schema"))
			if panel.Title != "Configure "+expected.String("title") || len(panel.Fields) != len(schema.OwnProperties()) {
				t.Fatal("original forced dialog consumer mismatch", i, panel, raw(want))
			}
			for _, field := range panel.Fields {
				value := obj(expected.Get("values")).String(field.Name)
				if string(obj(schema.Get(field.Name)).Get("sensitive")) == "true" {
					value = ""
				}
				if field.Value != value {
					t.Fatal("saved actual Source prefill mismatch", field, value)
				}
			}
		} else if panel.Title != "sample@mkt" || len(panel.Fields) > 0 {
			t.Fatal("original cancel/save detail transition mismatch", i, panel)
		}
		settings, _ := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
		secrets, _ := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".credentials.json"))
		for _, pair := range []struct{ got, want json.RawMessage }{{obj(settings).Get("pluginConfigs"), want.Get("pluginConfigs")}, {obj(secrets).Get("pluginSecrets"), want.Get("pluginSecrets")}} {
			a, b := any(map[string]any{}), any(map[string]any{})
			if pair.got != nil {
				_ = json.Unmarshal(pair.got, &a)
			}
			_ = json.Unmarshal(pair.want, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("original Manage storage consumer step%d %s Source%s", i, pair.got, pair.want)
			}
		}
	}
}
