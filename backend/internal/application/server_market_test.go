package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestDesktopMarketHTTPInstallsIntoActualNativeSkillRegistry(t *testing.T) {
	content := "---\nname: desktop-market-reader\ndescription: Physical desktop market skill\n---\nRead the exact file requested by the user.\n"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/file"):
			fmt.Fprint(w, content)
		case strings.Contains(r.URL.Path, "/versions/"):
			json.NewEncoder(w).Encode(map[string]any{"version": map[string]any{"version": "1.0", "files": []any{map[string]any{"path": "SKILL.md", "size": len(content)}}}})
		case strings.HasSuffix(r.URL.Path, "/desktop-market-reader"):
			json.NewEncoder(w).Encode(map[string]any{"skill": map[string]any{"slug": "desktop-market-reader", "displayName": "Desktop Market Reader", "description": "Actual Go desktop market skill"}, "latestVersion": map[string]any{"version": "1.0"}})
		default:
			t.Errorf("unexpected upstream %s", r.URL)
			http.Error(w, "missing", 404)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Environment["HAHA_MARKET_BASE_CLAWHUB"] = peer.URL
	o.Environment["HAHA_MARKET_BASE_SKILLHUB"] = peer.URL
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
		t.Error("Market accessed Keychain")
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
	call := func(method, path, body string, status int) protocol.JSONObject {
		t.Helper()
		req, e := http.NewRequest(method, host.URL+path, bytes.NewBufferString(body))
		if e != nil {
			t.Fatal(e)
		}
		response, e := host.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		data, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != status {
			t.Fatalf("%s %s: status%d %s %v", method, path, response.StatusCode, data, e)
		}
		return obj(data)
	}
	status := call("GET", "/api/market/status", "", 200)
	if status.Get("sources") == nil {
		t.Fatal("market status not actual API")
	}
	call("POST", "/api/market/install", `{"id":"clawhub:desktop-market-reader"}`, 200)
	physical := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "skills/desktop-market-reader/SKILL.md")
	data, e := os.ReadFile(physical)
	if e != nil || string(data) != content {
		t.Fatalf("actual Market physical producer: %v", e)
	}
	list := call("GET", "/api/skills", "", 200)
	if !bytes.Contains(list.Get("skills"), []byte("desktop-market-reader")) {
		t.Fatalf("installed market skill not native registry: %s", raw(list))
	}
	call("POST", "/api/market/uninstall", `{"id":"clawhub:desktop-market-reader"}`, 200)
	list = call("GET", "/api/skills", "", 200)
	if bytes.Contains(list.Get("skills"), []byte("desktop-market-reader")) {
		t.Fatal("removed market skill remained discoverable")
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/market/status", "", 499)
}
