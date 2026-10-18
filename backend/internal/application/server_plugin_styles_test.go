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

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopPluginStylesHTTPUsesActualRegistryAndSettingsInvalidation(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
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
	write(filepath.Join(config, "settings.json"), `{"enabledPlugins":{"sample@mkt":true},"future":{"keep":true}}`)
	write(filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"sample"}`)
	write(filepath.Join(plugin, "output-styles", "test.md"), "---\nname: Actual Plugin\ndescription: Plugin file content\n---\nUse the actual plugin prompt.")
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"mkt","owner":{"name":"Owner"},"plugins":[{"name":"sample","source":"./plugins/sample"}]}`)
	known, _ := json.Marshal(map[string]any{"mkt": map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}})
	write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(known))
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, host.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != status {
			t.Fatalf("%s %s: status=%d body=%s err=%v", method, path, resp.StatusCode, data, err)
		}
		return data
	}
	styles := request("GET", "/api/settings/output-styles", "", 200)
	if !bytes.Contains(styles, []byte(`"value":"sample:Actual Plugin"`)) || !bytes.Contains(styles, []byte(`"description":"Plugin file content"`)) {
		t.Fatalf("actual plugin style is absent: %s", styles)
	}
	request("PUT", "/api/settings/output-style", `{"outputStyle":"sample:Actual Plugin"}`, 200)
	request("PUT", "/api/settings/user", `{"enabledPlugins":{"sample@mkt":false}}`, 200)
	styles = request("GET", "/api/settings/output-styles", "", 200)
	if bytes.Contains(styles, []byte(`"value":"sample:Actual Plugin"`)) {
		t.Fatalf("disabled plugin still appears in cached styles: %s", styles)
	}
	request("PUT", "/api/settings/output-style", `{"outputStyle":"sample:Actual Plugin"}`, 400)
	request("PUT", "/api/settings/user", `{"enabledPlugins":{"sample@mkt":true}}`, 200)
	styles = request("GET", "/api/settings/output-styles", "", 200)
	if !bytes.Contains(styles, []byte(`"value":"sample:Actual Plugin"`)) {
		t.Fatalf("re-enabled plugin remains absent: %s", styles)
	}
	data, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || !bytes.Contains(data, []byte(`"keep": true`)) || !bytes.Contains(data, []byte(`"outputStyle": "sample:Actual Plugin"`)) {
		t.Fatalf("user fields lost: %s %v", data, err)
	}
	if entries, err := os.ReadDir(filepath.Join(config, "projects")); err == nil && len(entries) > 0 {
		t.Fatal("settings HTTP routes started a hidden Agent")
	}
}
