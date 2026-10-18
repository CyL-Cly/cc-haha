package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func prepareTerminalPluginFixture(t *testing.T, o Options) string {
	t.Helper()
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	market := filepath.Join(o.WorkDir, "market")
	plugin := filepath.Join(market, "plugins", "sample")
	write := func(path string, value []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config, "settings.json"), []byte(`{"futureUser":{"keep":true}}`))
	write(filepath.Join(plugin, ".claude-plugin", "plugin.json"), []byte(`{"name":"sample","description":"Actual public panel plugin","version":"1.0.0","userConfig":{"region":{"type":"string","title":"Region","description":"Actual next-turn region","required":true},"secret":{"type":"string","title":"Secret","description":"Private top option","sensitive":true}},"channels":[{"server":"chat","displayName":"Chat","userConfig":{"token":{"type":"string","title":"Token","description":"Private channel token","required":true,"sensitive":true}}}]}`))
	write(filepath.Join(plugin, "skills", "sample", "SKILL.md"), []byte("---\ndescription: Actual plugin Skill\n---\nACTUAL_PLUGIN_OPTION_${user_config.region}"))
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), []byte(`{"name":"mkt","owner":{"name":"Owner"},"plugins":[{"name":"sample","description":"PUBLIC_PLUGIN_ENTRY","source":"./plugins/sample"}]}`))
	write(filepath.Join(config, "plugins", "known_marketplaces.json"), raw(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}}))
	// The actual Source counts consumer reads a fresh physical cache. This also
	// proves that discovery does not need public network in a deterministic lane.
	write(filepath.Join(config, "plugins", "install-counts-cache.json"), raw(map[string]any{"version": 1, "fetchedAt": time.Now().UTC().Format(time.RFC3339Nano), "counts": []any{}}))
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("disposable fixture Keychain unavailable")
	}
	return plugin
}
func TestTerminalPluginPanelPhysicalInstallConfigurationReloadAndNextTurn(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests.Add(1)
		if !strings.Contains(string(body), "ACTUAL_PLUGIN_OPTION_North") {
			t.Error("saved option never reached same native skill", string(body))
		}
		response(w, "plugin-panel-next", "", nil, "PLUGIN_NEXT_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	prepareTerminalPluginFixture(t, o)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	snapshot := func() []tui.Completion { commands, _ := s.SubscribeCommands(nil); return commands }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var panel *tui.LocalPanel
	notice := ""
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.Kind == tui.Notice || event.Kind == tui.LocalCommandOutput {
			notice = event.Text
		}
		return nil
	}
	open := func(args string) {
		t.Helper()
		if handled, err := s.InvokeTerminalCommand(ctx, "plugin", args, emit); !handled || err != nil {
			t.Fatal(handled, err)
		}
	}
	action := func(name, row string, fields map[string]string) {
		t.Helper()
		if err := s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: name, Row: row, Fields: fields}, emit); err != nil {
			t.Fatal(name, err)
		}
		if strings.Contains(panel.Notice, "Failed") || strings.Contains(panel.Notice, "blocked") {
			t.Fatal(name, panel.Notice)
		}
	}
	open("install sample@mkt")
	if panel.Title != "sample@mkt" || len(panel.Rows) != 4 || panel.Rows[0].ID != "install" || panel.Rows[1].ID != "install-project" || panel.Rows[2].ID != "install-local" || panel.Rows[3].ID != "back" {
		t.Fatal(panel)
	}
	action("plugin-install", "install", nil)
	if panel.Title != "Configure sample" || len(panel.Fields) != 1 || panel.Fields[0].Name != "region" {
		t.Fatalf("actual userConfig not projected: %+v", panel)
	}
	action("save", "", map[string]string{"region": "North"})
	if panel.Title != "Configure Chat" || len(panel.Fields) != 1 || !panel.Fields[0].Sensitive {
		t.Fatal(panel)
	}
	action("save", "", map[string]string{"token": "PRIVATE_CHANNEL_SECRET"})
	open("manage")
	action("plugin-open", panel.Rows[0].ID, nil)
	action("plugin-configure", "options", nil)
	if len(panel.Fields) != 2 || !panel.Fields[1].Sensitive {
		t.Fatal("explicit Configure options must expose full top schema", panel)
	}
	action("save", "", map[string]string{"region": "North", "secret": "PRIVATE_TOP_SECRET"})
	if panel.Title != "sample@mkt" || len(panel.Fields) != 0 {
		t.Fatal("Configure options unexpectedly entered channel flow", panel)
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	data, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || !strings.Contains(string(data), `"region": "North"`) || strings.Contains(string(data), "PRIVATE_") {
		t.Fatal("configuration storage split", err, string(data))
	}
	secret, err := os.ReadFile(filepath.Join(config, ".credentials.json"))
	if err != nil || !strings.Contains(string(secret), "PRIVATE_TOP_SECRET") || !strings.Contains(string(secret), "PRIVATE_CHANNEL_SECRET") || !strings.Contains(string(secret), "sample@mkt/chat") {
		t.Fatal("distinct channel credential domain", err, string(secret))
	}
	if strings.Contains(string(raw(snapshot())), "sample:sample") {
		t.Fatal("install skipped Source explicit reload boundary")
	}
	if handled, err := s.InvokeTerminalCommand(ctx, "reload-plugins", "", emit); !handled || err != nil {
		t.Fatal(handled, err)
	}
	if !strings.Contains(notice, "Reloaded:") || !strings.Contains(string(raw(snapshot())), "sample:sample") {
		t.Fatal("same session command consumer stale", notice, string(raw(snapshot())))
	}
	if err = s.Submit(ctx, tui.Input{Text: "/sample:sample supplied arguments"}, nil); err != nil {
		t.Fatal(err)
	}
	open("disable sample@mkt")
	data, err = os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || !strings.Contains(string(data), `"sample@mkt": false`) || !strings.Contains(string(data), "futureUser") {
		t.Fatal("disable save/unknown preservation", err, string(data))
	}
	if _, err = s.InvokeTerminalCommand(ctx, "reload-plugins", "", emit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw(snapshot())), "sample:sample") {
		t.Fatal("disabled plugin still reachable")
	}
	open("enable sample@mkt")
	if _, err = s.InvokeTerminalCommand(ctx, "reload-plugins", "", emit); err != nil {
		t.Fatal(err)
	}
	open("manage")
	if len(panel.Rows) != 1 || !strings.Contains(panel.Rows[0].Label, "enabled") {
		t.Fatal(panel)
	}
	action("plugin-open", panel.Rows[0].ID, nil)
	action("plugin-configure", "options", nil)
	if panel.Fields[1].Value != "" {
		t.Fatal("secret prepopulated into product form")
	}
	action("save", "", map[string]string{"region": "North", "secret": ""})
	secret, err = os.ReadFile(filepath.Join(config, ".credentials.json"))
	if err != nil || !strings.Contains(string(secret), "PRIVATE_TOP_SECRET") || !strings.Contains(string(secret), "PRIVATE_CHANNEL_SECRET") {
		t.Fatal("blank reconfigure wiped secrets", err, string(secret))
	}
	open("uninstall sample@mkt")
	if _, err = s.InvokeTerminalCommand(ctx, "reload-plugins", "", emit); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || strings.Contains(string(raw(snapshot())), "sample:sample") {
		t.Fatal("local panels sent model text or uninstall left catalog", requests.Load(), "panel", panel.Title, "notice", panel.Notice)
	}
}
