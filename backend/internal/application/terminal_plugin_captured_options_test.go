package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPluginActualSourceCapturedOptionsAfterUpdateAndUninstall(t *testing.T) {
	bun, sdk := os.Getenv("CC_HAHA_SOURCE_BUN"), os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("pinned Source and verified MCPB reference required")
	}
	for _, operation := range []string{"update", "uninstall"} {
		t.Run(operation, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			work := filepath.Join(home, "work")
			files, _, _ := prepareTerminalBulkFixture(t, work, filepath.Join(home, ".claude"))
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-reload.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_RELOAD_SOURCE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "USER_TYPE=external", "COLUMNS=100", "LINES=35"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "workDir": work, "entrypoint": "cli", "capturedOptions": operation, "files": files}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual operation and captured flow %v %.5000s", err, output)
			}
			var source struct {
				View, Settings, Installed protocol.JSONObject
				Mounted                   string
				Outcomes                  [][]string
				ProcessError              *string
				Caps                      struct{ Blocked []string }
			}
			if err = json.Unmarshal(output, &source); err != nil || len(source.Caps.Blocked) > 0 || source.ProcessError != nil || source.View.String("type") != "plugin-options" || len(source.Outcomes) != 1 || source.Outcomes[0][0] != "configured" || !strings.Contains(source.Mounted, "Region") || strings.Contains(source.Mounted, "Replacement schema") {
				t.Fatal("Original actual captured manifest boundary", err, string(output))
			}
			installed := array(obj(source.Installed.Get("plugins")).Get("zeta@mkt"))
			if operation == "update" && (len(installed) != 1 || installed[0].String("version") != "2.0.0") || operation == "uninstall" && len(installed) != 0 {
				t.Fatal("Original physical operation did not complete", string(output))
			}
			var requestBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requestBody = string(body)
				response(w, "captured-options-next", "", nil, "CAPTURED_OPTIONS_NEXT_DONE")
			}))
			defer server.Close()
			o := nativeSessionOptions(t, server.URL)
			o.Interactive = true
			prepareTerminalBulkFixture(t, o.WorkDir, o.Environment["CLAUDE_CONFIG_DIR"])
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			ctx := context.Background()
			if _, err = s.process.TerminalPluginOperation(ctx, plugincli.Command{Action: "install", Argument: "zeta@mkt", Scope: "user"}); err != nil {
				t.Fatal(err)
			}
			var panel *tui.LocalPanel
			emit := func(event tui.Event) error {
				if event.LocalPanel != nil {
					value := *event.LocalPanel
					panel = &value
				}
				return nil
			}
			if _, err = s.InvokeTerminalCommand(ctx, "plugin", "manage", emit); err != nil {
				t.Fatal(err)
			}
			if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "plugin-open", Row: panel.Rows[0].ID}, emit); err != nil {
				t.Fatal(err)
			}
			selection := s.terminalPanel.pluginPanel.selection
			if _, err = s.process.TerminalSelectedPluginConfiguration(ctx, &clibootstrap.TerminalPluginSelection{}, false); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("forged selection admitted", err)
			}
			foreign := &clibootstrap.MainPrint{}
			if _, err = foreign.TerminalSelectedPluginConfiguration(ctx, selection, false); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("foreign selection admitted", err)
			}
			if err = foreign.SaveTerminalSelectedPluginConfiguration(ctx, selection, "", protocol.NewJSONObject()); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("foreign selected writer admitted", err)
			}
			if operation == "update" {
				path := filepath.Join(o.WorkDir, "market/plugins/zeta/.claude-plugin/plugin.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				changed := obj(data)
				changed.Set("version", raw("2.0.0"))
				changed.Set("userConfig", []byte(`{"replacement":{"type":"string","title":"Replacement schema","description":"Actual replacement option","required":true}}`))
				if err = os.WriteFile(path, raw(changed), 0600); err != nil {
					t.Fatal(err)
				}
			}
			operationRow := operation
			if operation == "update" {
				operationRow = "update-now"
			}
			if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "plugin-" + operation, Row: operationRow}, emit); err != nil {
				t.Fatal(err)
			}
			if len(panel.Fields) != 1 || panel.Fields[0].Name != "region" {
				t.Fatalf("actual Source keeps selected manifest after %s; Go fields%+v notice%q", operation, panel.Fields, panel.Notice)
			}
			if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "save", Fields: map[string]string{"region": "Captured region"}}, emit); err != nil || panel.Notice != terminalPluginPostEnableMessage("zeta", "configured", "") {
				t.Fatal("captured configuration save", err, panel)
			}
			data, err := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
			var actual, expected any
			_ = json.Unmarshal(obj(data).Get("pluginConfigs"), &actual)
			_ = json.Unmarshal(source.Settings.Get("pluginConfigs"), &expected)
			if err != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatal("Original captured configuration writer", err, string(data), string(raw(source.Settings)))
			}
			if err = s.Submit(ctx, tui.Input{Mode: "prompt", Text: "Continue after captured plugin configuration"}, emit); err != nil || !strings.Contains(requestBody, "Continue after captured plugin configuration") {
				t.Fatal("same Harness next ordinary input", err)
			}
			if err = s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = s.process.TerminalSelectedPluginConfiguration(ctx, selection, false); !errors.Is(err, cooperative.ErrClosed) {
				t.Fatal("closed selection started native work", err)
			}
		})
	}
}
