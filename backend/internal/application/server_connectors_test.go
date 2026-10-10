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
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopConnectorsHTTPUsesActualLazyNativeLifecycle(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	var securityCalls atomic.Int32
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
		securityCalls.Add(1)
		return nil, os.ErrPermission
	}, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
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
		req, err := http.NewRequest(method, host.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != status {
			t.Fatalf("%s %s status=%d body=%s err=%v", method, path, resp.StatusCode, data, err)
		}
		return obj(data)
	}
	request("POST", "/api/connectors/wecom/authenticate", `{"configuration":{"TOKEN":"bad\u0000value"}}`, 400)
	request("DELETE", "/api/connectors", "", 405)
	request("POST", "/api/connectors/wecom/unknown", `{}`, 404)
	if _, err := os.Stat(filepath.Join(config, "connectors")); !os.IsNotExist(err) {
		t.Fatal("invalid route initialized Connector persistence", err)
	}
	list := request("GET", "/api/connectors", "", 200)
	var items []protocol.JSONObject
	if err := json.Unmarshal(list.Get("items"), &items); err != nil {
		t.Fatal(err)
	}
	foundNative, foundSkill, foundRemote := false, false, false
	for _, item := range items {
		foundNative = foundNative || item.String("id") == "wecom"
		foundSkill = foundSkill || item.String("transport") == "skills"
		foundRemote = foundRemote || item.String("transport") == "mcp"
		if item.Get("supported") == nil || item.Get("installed") == nil || item.Get("status") == nil {
			t.Fatalf("catalog skipped actual lifecycle DTO %s", raw(item))
		}
	}
	if !foundNative || !foundSkill || !foundRemote {
		t.Fatalf("catalog families missing native=%v skill=%v remote=%v", foundNative, foundSkill, foundRemote)
	}
	detail := request("GET", "/api/connectors/wecom", "", 200)
	if obj(detail.Get("connector")).String("status") != "not-installed" {
		t.Fatal(string(raw(detail)))
	}
	request("GET", "/api/connectors/no-such-connector", "", 404)
	request("POST", "/api/connectors/wecom/authenticate", `{}`, 400)
	if securityCalls.Load() != 0 {
		t.Fatal("list/error routes touched native Keychain")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	request("GET", "/api/connectors", "", 499)
}
