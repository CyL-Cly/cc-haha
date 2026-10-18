package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopWorkflowAPIUsesActualNativeSettingsAndShutdown(t *testing.T) {
	var modelCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		modelCalls.Add(1)
		http.Error(w, "Workflow API may not start inference", 500)
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	write := func(path, text string) {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	write(filepath.Join(config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"future":{"keep":true}}`)
	write(filepath.Join(o.WorkDir, ".git", "HEAD"), "ref: refs/heads/main\n")
	o.Environment["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Environment["HOME"], "tmp")
	_ = os.MkdirAll(o.Environment["CLAUDE_CODE_TMPDIR"], 0700)
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(o.Environment["HOME"], "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	host := httptest.NewServer(s)
	defer host.Close()
	defer s.Close(context.Background())
	requestDesktopJSON := func(t *testing.T, host *httptest.Server, method, path, body string, status int) []byte {
		t.Helper()
		request, e := http.NewRequest(method, host.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		request.Header.Set("Content-Type", "application/json")
		response, e := host.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != status {
			t.Fatalf("%s %s status=%d body=%s error=%v", method, path, response.StatusCode, raw, e)
		}
		return raw
	}
	script := `export const meta={name:'native-desktop',description:'Saved through actual constructor'};return 1`
	raw, _ := json.Marshal(map[string]any{"script": script, "scope": "user"})
	response := requestDesktopJSON(t, host, "POST", "/api/workflows/save", string(raw), 200)
	if !strings.Contains(string(response), `"native-desktop"`) {
		t.Fatal(string(response))
	}
	if data, e := os.ReadFile(filepath.Join(config, "workflows", "native-desktop.js")); e != nil || string(data) != script {
		t.Fatal("desktop API did not physically save original script", e)
	}
	requestDesktopJSON(t, host, "GET", "/api/workflows/native-desktop", "", 200)
	requestDesktopJSON(t, host, "GET", "/api/workflows/runs", "", 200)
	requestDesktopJSON(t, host, "GET", "/api/workflows/session-runs/not-present", "", 200)
	settings, e := os.ReadFile(filepath.Join(config, "settings.json"))
	if e != nil || !strings.Contains(string(settings), `"future":{"keep":true}`) {
		t.Fatal("Workflow API changed user settings", e)
	}
	requestDesktopJSON(t, host, "PUT", "/api/settings/user", `{"disableWorkflows":true}`, 200)
	requestDesktopJSON(t, host, "GET", "/api/workflows", "", 403)
	if e := s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if modelCalls.Load() != 0 {
		t.Fatal("definition APIs started an Agent", modelCalls.Load())
	}
}
