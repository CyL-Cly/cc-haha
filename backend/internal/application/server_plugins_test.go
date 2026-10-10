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
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopPluginFactoryUsesSharedRegistryComponentsAndScopedSettings(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	config, home := o.Environment["CLAUDE_CONFIG_DIR"], o.Environment["HOME"]
	plugin := filepath.Join(config, "plugins", "cache", "factory", "sample", "1.0.0")
	market := filepath.Join(home, "market")
	write := func(path string, content []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config, "settings.json"), raw(map[string]any{"future": map[string]any{"keep": true}, "enabledPlugins": map[string]any{"sample@factory": true}}))
	write(filepath.Join(config, "plugins", "known_marketplaces.json"), raw(map[string]any{"factory": map[string]any{"source": map[string]any{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2026-10-01T00:00:00.000Z"}}))
	write(filepath.Join(config, "plugins", "installed_plugins.json"), raw(map[string]any{"version": 2, "plugins": map[string]any{"sample@factory": []any{map[string]any{"scope": "user", "installPath": plugin, "version": "1.0.0", "installedAt": "2026-10-01T00:00:00.000Z", "lastUpdated": "2026-10-01T00:00:00.000Z"}}}}))
	write(filepath.Join(market, ".claude-plugin", "marketplace.json"), raw(map[string]any{"name": "factory", "owner": map[string]any{"name": "Fixture"}, "plugins": []any{map[string]any{"name": "sample", "source": "./plugins/sample"}}}))
	manifest := raw(map[string]any{"name": "sample", "version": "1.0.0", "description": "Actual shared factory plugin"})
	for _, base := range []string{plugin, filepath.Join(market, "plugins", "sample")} {
		write(filepath.Join(base, ".claude-plugin", "plugin.json"), manifest)
		write(filepath.Join(base, "commands", "factory-command.md"), []byte("---\ndescription: Real command\n---\nRead the actual fixture.\n"))
		write(filepath.Join(base, "agents", "factory-worker.md"), []byte("---\nname: factory-worker\ndescription: Real plugin worker\ntools: Read\n---\nRead the actual fixture.\n"))
		write(filepath.Join(base, "skills", "factory-skill", "SKILL.md"), []byte("---\nname: factory-skill\ndescription: Real skill\n---\nRead the actual fixture.\n"))
	}
	service, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(service)
	t.Cleanup(func() {
		host.Close()
		if err := service.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	request := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, host.URL+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != status {
			t.Fatalf("%s %s %d %s %v", method, path, res.StatusCode, data, err)
		}
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(string(data), err)
		}
		return value
	}
	list := request("GET", "/api/plugins", "", 200)
	if list["summary"].(map[string]any)["enabled"] != float64(1) {
		t.Fatal(list)
	}
	detail := request("GET", "/api/plugins/detail?id=sample%40factory", "", 200)["detail"].(map[string]any)
	counts := detail["componentCounts"].(map[string]any)
	if counts["commands"] != float64(1) || counts["agents"] != float64(1) || counts["skills"] != float64(1) {
		t.Fatal("actual components missing", detail)
	}
	reload := request("POST", "/api/plugins/reload?sessionId=retired", "", 200)
	if reload["session"].(map[string]any)["reason"] != "not_running" {
		t.Fatal(reload)
	}
	totals := reload["summary"].(map[string]any)
	if totals["enabled"] != float64(1) || totals["skills"] != float64(1) || totals["agents"] != float64(7) {
		t.Fatal("shared component producers", reload)
	}
	agents := request("GET", "/api/agents", "", 200)
	if !strings.Contains(string(raw(agents)), "sample:factory-worker") {
		t.Fatal("plugin definition absent from shared Agent catalog", agents)
	}
	request("POST", "/api/plugins/disable", `{"id":"sample@factory","scope":"user"}`, 200)
	data, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || !bytes.Contains(data, []byte(`"keep"`)) || !bytes.Contains(data, []byte(`false`)) {
		t.Fatal(string(data), err)
	}
	list = request("GET", "/api/plugins", "", 200)
	if list["summary"].(map[string]any)["enabled"] != float64(0) {
		t.Fatal("stale plugin cache", list)
	}
	request("POST", "/api/plugins/enable", string(raw(map[string]any{"id": "sample@factory", "scope": "project", "cwd": o.WorkDir})), 200)
	request("POST", "/api/plugins/reload", "", 200)
	data, err = os.ReadFile(filepath.Join(o.WorkDir, ".claude", "settings.json"))
	if err != nil || !bytes.Contains(data, []byte(`true`)) {
		t.Fatal(string(data), err)
	}
	request("GET", "/api/plugins/detail", "", 400)
	request("GET", "/api/plugins/detail?id=missing%40factory", "", 404)
	request("POST", "/api/plugins/enable", `{"id":"sample@factory","scope":"project"}`, 400)
	request("POST", "/api/plugins/uninstall", `{"id":"sample@factory","scope":"user","keepData":true}`, 200)
	if _, err := os.Stat(plugin); err != nil {
		t.Fatal("keepData removed actual plugin cache", err)
	}
}
