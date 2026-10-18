package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPluginSourceScopesPersistActualInstallAndReloadNextSkill(t *testing.T) {
	for _, scope := range []string{"user", "project", "local"} {
		t.Run(scope, func(t *testing.T) {
			var requests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requests.Add(1)
				if !strings.Contains(string(body), "ACTUAL_PLUGIN_OPTION_"+scope) {
					t.Error("same Harness did not consume actual scoped plugin options")
				}
				response(w, "scoped-plugin-next", "", nil, "ACTUAL_SCOPED_PLUGIN_SKILL_DONE")
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Interactive = true
			prepareTerminalPluginFixture(t, o)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			var panel *tui.LocalPanel
			emit := func(event tui.Event) error {
				if event.LocalPanel != nil {
					props := *event.LocalPanel
					panel = &props
				}
				return nil
			}
			if handled, err := s.InvokeTerminalCommand(ctx, "plugins", "install sample@mkt", emit); !handled || err != nil || panel == nil {
				t.Fatal(handled, err, panel)
			}
			rowID := "install-" + scope
			if scope == "user" {
				rowID = "install"
			}
			if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "plugin-install-" + scope, Row: rowID}, emit); err != nil {
				t.Fatal(err)
			}
			if len(panel.Fields) != 1 || panel.Title != "Configure sample" {
				t.Fatal("post-install actual options missing", panel)
			}
			if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "save", Fields: map[string]string{"region": scope}}, emit); err != nil {
				t.Fatal(err)
			}
			if panel.Title != "Configure Chat" {
				t.Fatal("post-install actual channel options missing", panel)
			}
			if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "save", Fields: map[string]string{"token": "private-test-channel"}}, emit); err != nil {
				t.Fatal(err)
			}
			settingsPath := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
			if scope == "project" {
				settingsPath = filepath.Join(o.WorkDir, ".claude", "settings.json")
			} else if scope == "local" {
				settingsPath = filepath.Join(o.WorkDir, ".claude", "settings.local.json")
			}
			settings, err := os.ReadFile(settingsPath)
			if err != nil || string(obj(obj(settings).Get("enabledPlugins")).Get("sample@mkt")) != "true" {
				t.Fatal("selected Source scope not persisted", settingsPath, string(settings), err)
			}
			installed, err := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "plugins", "installed_plugins.json"))
			if err != nil || !strings.Contains(string(installed), `"scope": "`+scope+`"`) {
				t.Fatal("actual installation metadata has wrong scope", string(installed), err)
			}
			if requests.Load() != 0 {
				t.Fatal("plugin menu/config invoked a model", requests.Load())
			}
			if handled, err := s.InvokeTerminalCommand(ctx, "reload-plugins", "", emit); !handled || err != nil {
				t.Fatal(handled, err)
			}
			if err = s.Submit(ctx, tui.Input{Text: "/sample:sample scoped actual arguments", Mode: "prompt"}, nil); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 {
				t.Fatal("next skill did not use same Harness exactly once", requests.Load())
			}
			user, _ := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
			if string(obj(obj(user).Get("futureUser")).Get("keep")) != "true" {
				t.Fatal("scoped install overwrote unknown user setting", string(user))
			}
		})
	}
}
