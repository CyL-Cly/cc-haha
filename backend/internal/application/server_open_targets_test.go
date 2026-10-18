package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopOpenTargetsHTTPUsesNativeServiceAtActualRouter(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	o.WorkDir = filepath.Join(home, "work")
	if err := os.MkdirAll(o.WorkDir, 0700); err != nil {
		t.Fatal(err)
	}
	// No launch is attempted. Actual native discovery runs with an explicit
	// disposable HOME; assertions do not depend on the machine's installed apps.
	o.Environment["PATH"] = "/usr/bin:/bin"
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path, body string, status int) map[string]any {
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
		var data map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s %s: %d %+v", method, path, resp.StatusCode, data)
		}
		return data
	}
	list := request("GET", "/api/open-targets", "", 200)
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	if list["platform"] != platform || list["targets"] == nil || list["ttlMs"] != float64(30000) || list["cachedAt"].(float64) <= 0 {
		t.Fatal("actual Open Targets service not bound", list)
	}
	if got := request("POST", "/api/open-targets/open", `{"targetId":"finder","path":"   "}`, 400); got["error"] != "BAD_REQUEST" {
		t.Fatal(got)
	}
	request("POST", "/api/open-targets/open", `{"targetId":"finder","path":"`+filepath.ToSlash(filepath.Join(home, "missing-owned-file"))+`"}`, 400)
	request("DELETE", "/api/open-targets", "", 405)
	request("GET", "/api/open-targets/icons/", "", 400)
	request("GET", "/api/open-targets/unavailable", "", 404)
}
