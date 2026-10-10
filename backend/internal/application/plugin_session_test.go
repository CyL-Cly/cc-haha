package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeSessionLoadsActualPluginCommandsAndRefreshesInPlace(t *testing.T) {
	var captured []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		response(w, "plugin-session", "", nil, "PLUGIN SESSION OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.SystemPrompt = ""
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	market := filepath.Join(o.WorkDir, "market")
	plugin := filepath.Join(market, "plugins", "sample")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config, "settings.json"), `{"enabledPlugins":{"sample@mkt":true},"outputStyle":"sample:Actual Plugin","future":{"keep":true}}`)
	write(filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"sample"}`)
	write(filepath.Join(plugin, "skills", "sample", "SKILL.md"), "---\ndescription: Actual plugin Skill\n---\nRead real project instructions.")
	write(filepath.Join(plugin, "output-styles", "test.md"), "---\nname: Actual Plugin\n---\nActual plugin output instructions.")
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[{"name":"sample","source":"./plugins/sample"}]}`)
	known, _ := json.Marshal(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}})
	write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(known))
	var init protocol.JSONObject
	o.OnSDK = func(data json.RawMessage) error {
		message := obj(data)
		if message.String("subtype") == "init" {
			init = message
		}
		return nil
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Text: "Use current plugin context."}, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(captured, []byte("Actual plugin output instructions.")) {
		t.Fatalf("plugin style never reached actual Harness request: %s", captured)
	}
	if !bytes.Contains(init.Get("slash_commands"), []byte(`"name":"sample:sample"`)) || !bytes.Contains(init.Get("plugins"), []byte(`"source":"sample@mkt"`)) || !bytes.Contains(init.Get("skills"), []byte(`"sample:sample"`)) {
		t.Fatalf("actual init omitted loaded plugin: %s", raw(init))
	}
	id := s.process.Controller.SessionID()
	write(filepath.Join(config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`)
	reloaded, err := s.process.ReloadComponents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reloaded.Get("commands"), []byte("sample:sample")) || len(s.process.Registry.Catalog().Plugins) != 0 || s.process.Controller.SessionID() != id {
		t.Fatalf("refresh did not remove disabled plugin in same session: %s", raw(reloaded))
	}
	write(filepath.Join(config, "settings.json"), `{"enabledPlugins":{"sample@mkt":true},"future":{"keep":true}}`)
	reloaded, err = s.process.ReloadComponents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(reloaded.Get("commands"), []byte("sample:sample")) || !bytes.Contains(reloaded.Get("plugins"), []byte("sample@mkt")) {
		t.Fatalf("re-enable remains stale: %s", raw(reloaded))
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Continue after reloading this session."}, nil); err != nil {
		t.Fatal("next actual ASK rejected refreshed settings", err)
	}
}
