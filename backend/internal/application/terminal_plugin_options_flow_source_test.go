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

	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalPluginPostEnableActualSourceMissingTopSubsetAndWholeChannelSchema(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	manifest := obj([]byte(`{"userConfig":{"region":{"type":"string","title":"Region","description":"Actual next-turn region","required":true},"secret":{"type":"string","title":"Secret","description":"Private top option","sensitive":true}},"channels":[{"server":"chat","displayName":"Chat","userConfig":{"token":{"type":"string","title":"Token","description":"Private channel token","required":true,"sensitive":true}}}]}`))
	saved := []protocol.JSONObject{obj([]byte(`{}`)), obj([]byte(`{"region":"North"}`)), obj([]byte(`{"region":17,"secret":42}`))}
	rows := []any{}
	for _, value := range saved {
		rows = append(rows, map[string]any{"plugin": map[string]any{"name": "sample", "source": "sample@mkt", "repository": "sample@mkt", "manifest": manifest}, "saved": value, "channels": map[string]any{}})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-options-flow.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PLUGIN_OPTIONS_FLOW_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("original post-enable options %v %.4000s", err, output)
	}
	var wants []struct {
		Top      protocol.JSONObject
		Channels []struct {
			Server string
			Schema protocol.JSONObject
		}
	}
	if err = json.Unmarshal(output, &wants); err != nil {
		t.Fatal(err, string(output))
	}
	for index, value := range saved {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			prepareTerminalPluginFixture(t, o)
			settingsPath := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
			_ = os.WriteFile(settingsPath, raw(map[string]any{"futureUser": map[string]any{"keep": true}, "pluginConfigs": map[string]any{"sample@mkt": map[string]any{"options": value}}}), 0600)
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if _, err = s.process.TerminalPluginOperation(context.Background(), plugincli.Command{Action: "install", Argument: "sample@mkt", Scope: "user"}); err != nil {
				t.Fatal(err)
			}
			config, err := s.process.TerminalPluginConfiguration(context.Background(), "sample@mkt", false)
			if err != nil {
				t.Fatal(err)
			}
			actual, expected := []protocol.JSONObject{}, []protocol.JSONObject{}
			for _, row := range array(config.Get("steps")) {
				actual = append(actual, protocol.NewJSONObject(protocol.JSONField{Name: "server", Value: row.Get("server")}, protocol.JSONField{Name: "schema", Value: row.Get("schema")}))
			}
			if len(wants[index].Top.OwnProperties()) > 0 {
				expected = append(expected, protocol.NewJSONObject(protocol.JSONField{Name: "server", Value: raw("")}, protocol.JSONField{Name: "schema", Value: raw(wants[index].Top)}))
			}
			for _, row := range wants[index].Channels {
				expected = append(expected, protocol.NewJSONObject(protocol.JSONField{Name: "server", Value: raw(row.Server)}, protocol.JSONField{Name: "schema", Value: raw(row.Schema)}))
			}
			var a, b any
			_ = json.Unmarshal(raw(actual), &a)
			_ = json.Unmarshal(raw(expected), &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("actual post-enable field walk differs: %s Source %s", raw(actual), raw(expected))
			}
		})
	}
}
