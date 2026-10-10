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
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

// This proves the actual factory binds one desktop-managed OAuth service into
// both REST discovery and the shared WS runtime transition. A logged-out
// account must serve its bundled catalog without any model/provider request.
func TestActualDesktopGrokProviderActivationModelsAndWSBinding(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path, body string, status int) protocol.JSONObject {
		t.Helper()
		r, err := http.NewRequest(method, host.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		response, err := host.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s %s %d %s %v", method, path, response.StatusCode, data, err)
		}
		return obj(data)
	}
	request("POST", "/api/providers/grok-official/activate", "{}", 200)
	status := request("GET", "/api/providers/auth-status", "", 200)
	if string(status.Get("hasAuth")) != "false" || status.String("source") != "none" || status.String("activeProvider") != "Grok Official" {
		t.Fatalf("OAuth incorrectly borrowed Anthropic credentials: %s", raw(status))
	}
	models := request("GET", "/api/models", "", 200)
	var catalog []protocol.JSONObject
	if err = json.Unmarshal(models.Get("models"), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 4 || catalog[0].String("id") != "grok-4.7" || obj(models.Get("provider")).String("id") != "grok-official" {
		t.Fatal("actual factory catalog is not the selected account's bundled catalog", string(raw(models)))
	}
	current := request("GET", "/api/models/current", "", 200)
	if obj(current.Get("model")).String("id") != "grok-4.7" {
		t.Fatal(string(raw(current)))
	}
	request("PUT", "/api/models/current", `{"modelId":"grok-4.5"}`, 200)
	current = request("GET", "/api/models/current", "", 200)
	if obj(current.Get("model")).String("name") != "Grok 4.5" {
		t.Fatal(string(raw(current)))
	}
	created := request("POST", "/api/sessions", string(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "default"})), 201)
	id := created.String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(20 * time.Second))
	if err = websocket.Message.Send(ws, string(raw(map[string]any{"type": "set_runtime_config", "providerId": "grok-official", "modelId": "grok-4.7", "effortLevel": "high"}))); err != nil {
		t.Fatal(err)
	}
	applied := false
	for steps := 0; steps < 100; steps++ {
		var line string
		if err = websocket.Message.Receive(ws, &line); err != nil {
			t.Fatal(err)
		}
		m := obj([]byte(line))
		if m.String("type") == "error" {
			t.Fatal("actual WS runtime failed", line)
		}
		if m.String("type") == "runtime_config_applied" {
			if m.String("providerId") != "grok-official" || m.String("modelId") != "grok-4.7" || m.String("effortLevel") != "high" {
				t.Fatal(line)
			}
			applied = true
			break
		}
	}
	if !applied {
		t.Fatal("actual WS runtime did not acknowledge selected OAuth model")
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config, "cc-haha", "settings.json"))
	if err != nil || !bytes.Contains(data, []byte(`"grok_oauth"`)) || bytes.Contains(data, []byte("fake-only-loopback")) {
		t.Fatal("OAuth activation settings retained another provider's key", err, string(data))
	}
	if _, err = os.Stat(filepath.Join(config, "cc-haha", "grok-oauth.json")); !os.IsNotExist(err) {
		t.Fatal("logged-out model discovery manufactured credentials", err)
	}
}
