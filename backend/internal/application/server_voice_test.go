package application

import (
	"bytes"
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopVoiceFactoryUsesOriginalRoutesAndSharedPreferencesWithoutModel(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	path := filepath.Join(config, "cc-haha", "desktop-ui.json")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte(`{"schemaVersion":3,"voiceInput":{"enabled":false,"providerId":"sensevoice-local","language":"auto","futureVoice":{"keep":true}},"futureDesktop":{"keep":true}}`), 0600)
	s, e := NewDesktopServer(ServerOptions{HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Environment: o.Environment, Arguments: o.Arguments, Version: o.Version, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	host := httptest.NewServer(s)
	defer host.Close()
	t.Cleanup(func() {
		if e := s.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	request := func(method, path string, data []byte, status int) protocol.JSONObject {
		t.Helper()
		req, _ := http.NewRequest(method, host.URL+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		res, e := host.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("%s %s %d %s", method, path, res.StatusCode, body)
		}
		return obj(body)
	}
	catalog := request("GET", "/api/voice/catalog", nil, 200)
	if string(catalog.Get("supported")) != "true" || !bytes.Contains(catalog.Get("providers"), []byte("sensevoice-local")) {
		t.Fatal(string(raw(catalog)))
	}
	updated := request("PUT", "/api/voice/preferences", []byte(`{"enabled":true,"language":"zh","downloadSource":"mirror"}`), 200)
	if !bytes.Contains(updated.Get("preferences"), []byte("futureVoice")) {
		t.Fatal("shared preference unknown fields lost", string(raw(updated)))
	}
	disk, e := os.ReadFile(path)
	if e != nil || !bytes.Contains(disk, []byte("futureDesktop")) || !bytes.Contains(disk, []byte(`"downloadSource": "mirror"`)) {
		t.Fatal("original shared preference file", e, string(disk))
	}
	request("GET", "/api/voice/providers/sensevoice-local/status", nil, 200)
	request("GET", "/api/voice/providers/unknown/status", nil, 404)
	request("POST", "/api/voice/transcribe", []byte("not WAV"), 400)
	request("DELETE", "/api/voice/providers/sensevoice-local/assets", nil, 200)
	if _, e = os.Stat(filepath.Join(config, "cc-haha", "voice", "sensevoice")); !os.IsNotExist(e) {
		t.Fatal("asset remove did not consume actual private layout", e)
	}
}
