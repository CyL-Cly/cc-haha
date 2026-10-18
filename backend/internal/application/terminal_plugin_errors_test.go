package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPluginErrorsActualSourcesRemoveDeclarationsKnownCacheAndNextHuman(t *testing.T) {
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		response(w, "errors-next", "", nil, "ERRORS_NEXT_HUMAN")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Interactive = true
	prepareTerminalPluginFixture(t, o)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	user := filepath.Join(config, "settings.json")
	project := filepath.Join(o.WorkDir, ".claude", "settings.json")
	missing := filepath.Join(o.WorkDir, "missing-private-market")
	for _, path := range []string{user, project} {
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		if err := os.WriteFile(path, raw(map[string]any{"futureSource": map[string]any{"keep": true}, "extraKnownMarketplaces": map[string]any{"ghost": map[string]any{"source": map[string]any{"source": "directory", "path": missing}}}, "enabledPlugins": map[string]any{"bad@ghost": true, "keep@mkt": false}}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	knownPath := filepath.Join(config, "plugins", "known_marketplaces.json")
	data, err := os.ReadFile(knownPath)
	if err != nil {
		t.Fatal(err)
	}
	known := obj(data)
	known.Set("broken", raw(map[string]any{"source": map[string]any{"source": "directory", "path": missing + "-broken"}, "installLocation": missing + "-broken", "lastUpdated": "2026-10-01T00:00:00.000Z"}))
	_ = os.WriteFile(knownPath, raw(known), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	changes := make(chan struct{}, 1)
	status, remove, err := s.process.SubscribeNativePluginInstallation(ctx, func(value protocol.JSONObject) {
		select {
		case changes <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	failed := func(value protocol.JSONObject) bool {
		for _, row := range array(value.Get("marketplaces")) {
			if row.String("name") == "ghost" && row.String("status") == "failed" {
				return true
			}
		}
		return false
	}
	for !failed(status) {
		select {
		case <-ctx.Done():
			t.Fatal("actual startup marketplace never failed", raw(status))
		case <-changes:
		}
		status, err = s.process.NativePluginInstallationStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	handled, err := s.InvokeTerminalCommand(ctx, "plugin", "", emit)
	if !handled || err != nil {
		t.Fatal(handled, err)
	}
	apply := func(action, row string) {
		t.Helper()
		if err := s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: action, Row: row}, emit); err != nil {
			t.Fatal(action, err)
		}
	}
	apply("plugin-tab", "")
	apply("plugin-tab", "")
	apply("plugin-tab", "")
	find := func(label string) string {
		t.Helper()
		for _, row := range panel.Rows {
			if strings.HasPrefix(row.Label, label) {
				return row.ID
			}
		}
		t.Fatalf("actual Errors row %s absent: %+v", label, panel)
		return ""
	}
	apply("plugin-error", find("ghost"))
	status, err = s.process.NativePluginInstallationStatus(ctx)
	if err != nil || failed(status) {
		t.Fatal("removed failed status survived actual Source action", err, raw(status))
	}
	for _, path := range []string{user, project} {
		data, err := os.ReadFile(path)
		settings := obj(data)
		if err != nil || obj(settings.Get("extraKnownMarketplaces")).Get("ghost") != nil || obj(settings.Get("enabledPlugins")).Get("bad@ghost") != nil || string(obj(settings.Get("enabledPlugins")).Get("keep@mkt")) != "false" || string(obj(settings.Get("futureSource")).Get("keep")) != "true" {
			t.Fatal("error action did not remove exact declared scope/preserve unknown", path, err, string(data))
		}
	}
	apply("plugin-error", find("broken"))
	data, err = os.ReadFile(knownPath)
	if err != nil || obj(data).Get("broken") != nil || obj(data).Get("mkt") == nil {
		t.Fatal("actual broken known market not removed narrowly", err, string(data))
	}
	if requests.Load() != 0 {
		t.Fatal("errors menu called model", requests.Load())
	}
	if err = s.Submit(ctx, tui.Input{Text: "next ordinary after real errors remediation", Mode: "prompt"}, nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("remediation altered next Harness", requests.Load())
	}
}
