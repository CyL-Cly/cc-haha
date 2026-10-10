package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopServerBindsNativeOutputStylesWithoutStartingAgent(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config, "output-styles", "native.md"), "---\nname: Native Custom\ndescription: Actual native style\n---\nKeep the actual style prompt.")
	write(filepath.Join(config, "settings.json"), `{"outputStyle":"Native Custom","language":"中文","future":{"keep":true}}`)
	managed := o.Native.ManagedRoot
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	response, err := host.Client().Get(host.URL + "/api/settings/output-styles")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"outputStyle":"Native Custom"`)) || !bytes.Contains(data, []byte(`"description":"Actual native style"`)) {
		t.Fatalf("native styles %d %s", response.StatusCode, data)
	}
	request, _ := http.NewRequest(http.MethodPut, host.URL+"/api/settings/output-style", bytes.NewReader([]byte(`{"outputStyle":"Learning"}`)))
	request.Header.Set("Content-Type", "application/json")
	response, err = host.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save %d %s", response.StatusCode, data)
	}
	persisted, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || !bytes.Contains(persisted, []byte(`"outputStyle": "Learning"`)) || !bytes.Contains(persisted, []byte(`"keep": true`)) {
		t.Fatalf("native save lost source fields: %s %v", persisted, err)
	}
	if sessions, err := os.ReadDir(filepath.Join(config, "projects")); err == nil && len(sessions) != 0 {
		t.Fatal("style API started a hidden Agent", sessions)
	}
}
