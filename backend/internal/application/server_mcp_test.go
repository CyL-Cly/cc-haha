package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopMCPManagementHTTPAgainstActualSource(t *testing.T) {
	var initializations, toolLists atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		message := obj(data)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		result := map[string]any{}
		w.Header().Set("Content-Type", "application/json")
		if message.String("method") == "initialize" {
			initializations.Add(1)
			result = map[string]any{"protocolVersion": "2025-11-25", "serverInfo": map[string]string{"name": "private-peer", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		} else if message.String("method") == "tools/list" {
			toolLists.Add(1)
			_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "error": map[string]any{"code": -32603, "message": "private discovery error"}}))
			return
		}
		_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result}))
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Arguments.Tools = []string{""}
	o.Environment["CC_HAHA_DISABLE_TERMINAL_SHELL_ENV"] = "1"
	canonical, err := filepath.EvalSymlinks(o.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	o.WorkDir = canonical
	home, err := filepath.EvalSymlinks(o.Environment["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	o.Environment["HOME"], o.Environment["USERPROFILE"], o.Environment["CLAUDE_CONFIG_DIR"] = home, home, config
	files := map[string]string{filepath.Join(config, "settings.json"): `{"enableAllProjectMcpServers":true}`, filepath.Join(config, ".claude.json"): `{"future":"preserve"}`, filepath.Join(canonical, ".mcp.json"): `{"future":{"keep":true},"mcpServers":{"existing":{"command":"/usr/bin/true","args":["${UNEXPANDED}"],"future":"preserve"}}}`}
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	hostService, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: canonical, Arguments: o.Arguments, Version: o.Version, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer hostService.Close(context.Background())
	host := httptest.NewServer(hostService)
	defer host.Close()
	query := "?cwd=" + url.QueryEscape(canonical)
	other := filepath.Join(canonical, "other")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	otherQuery := "?cwd=" + url.QueryEscape(other)
	cases := []map[string]any{
		{"method": "GET", "path": "/api/mcp" + query},
		{"method": "GET", "path": "/api/mcp/project-paths"},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{"name":"local","scope":"local","config":{"type":"stdio","command":" /usr/bin/true ","args":["","  ",3,"a"],"env":{"KEEP":"v","IGNORE":2}}}`},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{"name":"local","scope":"local","config":{"command":"/usr/bin/true"}}`},
		{"method": "PUT", "path": "/api/mcp/local" + query, "body": `{"scope":"user","config":{"type":"http","url":" https://offline.invalid/service ","headers":{"X":"y","bad":2},"oauth":{"clientId":" public ","callbackPort":"0x1000"}}}`},
		{"method": "GET", "path": "/api/mcp" + query},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{"name":"project","scope":"project","config":{"type":"stdio","command":"/usr/bin/true"}}`},
		{"method": "GET", "path": "/api/mcp/project-paths"},
		{"method": "POST", "path": "/api/mcp/project/toggle" + query, "body": `{}`},
		{"method": "GET", "path": "/api/mcp/project/status" + query},
		{"method": "DELETE", "path": "/api/mcp/project" + query + "&scope=project"},
		{"method": "DELETE", "path": "/api/mcp/local" + query + "&scope=user"},
		{"method": "GET", "path": "/api/mcp" + query},
		{"method": "GET", "path": "/api/mcp/missing/status" + query},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{`},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{"name":"bad name","config":{"command":"/usr/bin/true"}}`},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{"name":"missing","config":{"command":"go-fixture-no-such-host-command"}}`},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{"name":"reserved","scope":"bad","config":{"type":"http","url":"url"}}`},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{"name":"remote","config":{"type":"http","url":"url","oauth":{"callbackPort":-2}}}`},
		{"method": "GET", "path": "/api/mcp/no-action" + query},
		{"method": "POST", "path": "/api/mcp" + query, "body": string(raw(map[string]any{"name": "probe", "scope": "user", "config": map[string]any{"type": "http", "url": peer.URL}}))},
		{"method": "GET", "path": "/api/mcp/probe/status" + query},
		{"method": "GET", "path": "/api/mcp/probe/status" + query},
		{"method": "POST", "path": "/api/mcp/probe/reconnect" + query, "body": "{}"},
		{"method": "POST", "path": "/api/mcp/probe/toggle" + query, "body": "{}"},
		{"method": "POST", "path": "/api/mcp" + query, "body": `{"name":"move","scope":"local","config":{"command":"/usr/bin/true","args":["old"]}}`},
		{"method": "POST", "path": "/api/mcp" + otherQuery, "body": `{"name":"move","scope":"local","config":{"command":"/usr/bin/true","args":["retained"]}}`},
		{"method": "PUT", "path": "/api/mcp/move" + otherQuery, "body": string(raw(map[string]any{"previousCwd": canonical, "scope": "local", "config": map[string]any{"command": "/usr/bin/true", "args": []string{"new"}}}))},
		{"method": "GET", "path": "/api/mcp" + query},
		{"method": "GET", "path": "/api/mcp" + otherQuery},
		{"method": "DELETE", "path": "/api/mcp/move" + query + "&scope=local"},
		{"method": "DELETE", "path": "/api/mcp/move" + otherQuery + "&scope=local"},
		{"method": "GET", "path": "/api/mcp/probe/status" + query},
		{"method": "POST", "path": "/api/mcp/probe/toggle" + query, "body": "{}"},
	}
	observations := []any{}
	for _, step := range cases {
		body, _ := step["body"].(string)
		request, err := http.NewRequest(step["method"].(string), host.URL+step["path"].(string), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer fake-only-loopback")
		response, err := host.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if json.Unmarshal(data, &value) != nil {
			t.Fatal(string(data))
		}
		observations = append(observations, map[string]any{"status": float64(response.StatusCode), "body": value})
		step["action"] = "api"
		step["settings"] = json.RawMessage(`{"enableAllProjectMcpServers":true}`)
	}
	data, err := os.ReadFile(filepath.Join(canonical, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	preserved := obj(data)
	if string(obj(preserved.Get("future")).Get("keep")) != "true" || !bytes.Contains(preserved.Get("mcpServers"), []byte("${UNEXPANDED}")) {
		t.Fatal("raw rewrite damaged unrelated configuration", string(data))
	}
	if err = hostService.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	goInitializations, goLists := initializations.Swap(0), toolLists.Swap(0)
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual Source HTTP comparison requires pinned Bun")
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-mcp-config.fixture.ts"))
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_MCP_CONFIG_FIXTURE=1", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
	input, _ := json.Marshal(map[string]any{"runtime": map[string]any{"cwd": canonical, "files": files, "version": o.Version}, "cases": cases})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source MCP API: %v\n%s", err, stderr.String())
	}
	var expected []any
	if json.Unmarshal(source, &expected) != nil {
		t.Fatal(string(source))
	}
	for i := range observations {
		if !reflect.DeepEqual(observations[i], expected[i]) {
			a, _ := json.Marshal(expected[i])
			b, _ := json.Marshal(observations[i])
			t.Fatalf("case %d %v\nSource %s\nGo %s", i, cases[i], a, b)
		}
	}
	if goInitializations != initializations.Load() || goLists != toolLists.Load() {
		t.Fatalf("probe lifecycle changed: Go initialize=%d tools/list=%d Source initialize=%d tools/list=%d", goInitializations, goLists, initializations.Load(), toolLists.Load())
	}
}
