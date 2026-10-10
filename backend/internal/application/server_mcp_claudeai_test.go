package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopClaudeAIMCPActualSourceListStatusReconnectAndSavedCredentials(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	var catalogs, initializes atomic.Int32
	proxy, ca := claudeAIAPIPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-private-api-token" {
			t.Error("saved credential not used", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/v1/mcp_servers" {
			catalogs.Add(1)
			_, _ = io.WriteString(w, `{"data":[{"id":"z-id","display_name":"Zulu","url":"https://vendor.invalid/z"},{"id":"a-id","display_name":"alpha","url":"https://vendor.invalid/a"}]}`)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/mcp/") {
			w.WriteHeader(405)
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		if r.Method != "POST" || (r.URL.Path != "/v1/mcp/z-id" && r.URL.Path != "/v1/mcp/a-id") {
			t.Error("unexpected physical request", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		data, _ := io.ReadAll(r.Body)
		message := obj(data)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		result := map[string]any{"tools": []any{}}
		if message.String("method") == "initialize" {
			initializes.Add(1)
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "owned-peer", "version": "1"}}
		}
		_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result}))
	}))
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, err := filepath.EvalSymlinks(o.Environment["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(home, "api-work")
	if err = os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	payload := `{"claudeAiOauth":{"accessToken":"fake-private-api-token","refreshToken":"fake-private-refresh","expiresAt":9999999999999,"scopes":["user:inference","user:mcp_servers"],"subscriptionType":"pro"},"future":{"preserve":true}}`
	files := map[string]string{filepath.Join(config, "settings.json"): `{}`, filepath.Join(config, ".claude.json"): `{"future":"preserve"}`, filepath.Join(config, ".credentials.json"): payload}
	for p, data := range files {
		if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	o.Environment["HOME"], o.Environment["USERPROFILE"], o.Environment["CLAUDE_CONFIG_DIR"] = home, home, config
	o.Environment["HTTPS_PROXY"], o.Environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
	o.Environment["NO_PROXY"] = "127.0.0.1,localhost"
	o.Environment["CC_HAHA_DISABLE_TERMINAL_SHELL_ENV"] = "1"
	hostService, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: cwd, Arguments: o.Arguments, Version: "fixture", RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer hostService.Close(context.Background())
	host := httptest.NewServer(hostService)
	defer host.Close()
	query := "?cwd=" + url.QueryEscape(cwd)
	steps := []map[string]string{{"method": "GET", "path": "/api/mcp" + query}, {"method": "GET", "path": "/api/mcp/" + url.PathEscape("claude.ai alpha") + "/status" + query}, {"method": "POST", "path": "/api/mcp/" + url.PathEscape("claude.ai Zulu") + "/reconnect" + query, "body": "{}"}, {"method": "GET", "path": "/api/mcp" + query}}
	actual := []any{}
	for _, step := range steps {
		req, err := http.NewRequest(step["method"], host.URL+step["path"], strings.NewReader(step["body"]))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		var body any
		if json.Unmarshal(data, &body) != nil {
			t.Fatal(string(data))
		}
		actual = append(actual, map[string]any{"status": float64(response.StatusCode), "body": body})
	}
	if err = hostService.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	goCatalogs, goInitializes := catalogs.Swap(0), initializes.Swap(0)
	path, _ := filepath.Abs("../../testdata/mcp-api-claudeai.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_CLAUDEAI_API_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "HTTPS_PROXY=" + proxy, "NODE_EXTRA_CA_CERTS=" + ca, "NO_PROXY=127.0.0.1,localhost", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
	input, _ := json.Marshal(map[string]any{"cwd": cwd, "files": files, "steps": steps})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source API", err, stderr.String())
	}
	var expected []any
	if json.Unmarshal(source, &expected) != nil {
		t.Fatal(string(source))
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("actual Source %s\nGo %s", source, raw(actual))
	}
	if goCatalogs != 1 || catalogs.Load() != 1 || goInitializes != 3 || initializes.Load() != 3 {
		t.Fatal("physical producer/lifecycle counts changed", goCatalogs, catalogs.Load(), goInitializes, initializes.Load())
	}
	data, _ := os.ReadFile(filepath.Join(config, ".credentials.json"))
	if string(data) != payload {
		t.Fatal("saved credential state changed")
	}
}

// Each profile executes the original public API in a separate process against
// the same physical TLS endpoints, then compares complete response bodies.
func TestDesktopClaudeAIMCPSourceAcrossCWDManualDedupToggleAndFailureMemo(t *testing.T) {
	for _, profile := range []string{"cross-cwd", "manual-dedup", "disabled-manual", "toggle", "failed-memo", "encoded-names", "enterprise", "raw-display-names"} {
		t.Run(profile, func(t *testing.T) {
			var catalogs, initializes atomic.Int32
			proxy, ca := claudeAIAPIPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fake-private-api-token" {
					t.Error("saved token transport not used")
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/mcp_servers" {
					catalogs.Add(1)
					if profile == "failed-memo" {
						w.WriteHeader(503)
						io.WriteString(w, `{"error":"owned-unavailable"}`)
						return
					}
					if profile == "raw-display-names" {
						io.WriteString(w, `{"data":[{"id":"r","display_name":"\ufffd","url":"https://vendor.invalid/r"},{"id":"s","display_name":"\ud800","url":"https://vendor.invalid/s"},{"id":"l","display_name":"alpha","url":"https://vendor.invalid/l"},{"id":"u","display_name":"Alpha","url":"https://vendor.invalid/u"}]}`)
						return
					}
					io.WriteString(w, `{"data":[{"id":"z","display_name":"Zulu","url":"https://vendor.invalid/z"},{"id":"a","display_name":"alpha","url":"https://vendor.invalid/a"},{"id":"p","display_name":"100%20 literal","url":"https://vendor.invalid/p"},{"id":"e","display_name":"éclair","url":"https://vendor.invalid/e"}]}`)
					return
				}
				if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/mcp/") {
					w.WriteHeader(405)
					return
				}
				if r.Method == "DELETE" {
					w.WriteHeader(204)
					return
				}
				if r.Method != "POST" {
					t.Errorf("unexpected peer %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
					return
				}
				data, _ := io.ReadAll(r.Body)
				message := obj(data)
				if message.Get("id") == nil {
					w.WriteHeader(202)
					return
				}
				result := map[string]any{"tools": []any{}}
				if message.String("method") == "initialize" {
					initializes.Add(1)
					result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "owned", "version": "1"}}
				}
				w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result}))
			}))
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			home, e := filepath.EvalSymlinks(o.Environment["HOME"])
			if e != nil {
				t.Fatal(e)
			}
			config := filepath.Join(home, ".claude")
			cwd := filepath.Join(home, "work")
			other := filepath.Join(home, "other")
			os.MkdirAll(cwd, 0700)
			os.MkdirAll(other, 0700)
			credential := `{"claudeAiOauth":{"accessToken":"fake-private-api-token","refreshToken":"fake-private-refresh","expiresAt":9999999999999,"scopes":["user:inference","user:mcp_servers"],"subscriptionType":"pro"},"future":{"keep":true}}`
			global := `{"future":"keep"}`
			if profile == "manual-dedup" || profile == "disabled-manual" {
				global = `{"future":"keep","mcpServers":{"manual":{"type":"http","url":"https://vendor.invalid/a"}}}`
			}
			if profile == "disabled-manual" {
				global = `{"future":"keep","mcpServers":{"manual":{"type":"http","url":"https://vendor.invalid/a"}},"projects":{"` + cwd + `":{"disabledMcpServers":["manual"]}}}`
			}
			files := map[string]string{filepath.Join(config, "settings.json"): `{}`, filepath.Join(config, ".claude.json"): global, filepath.Join(config, ".credentials.json"): credential}
			managedRoot := filepath.Join(home, "managed")
			if profile == "enterprise" {
				files[filepath.Join(managedRoot, "managed-mcp.json")] = `{"mcpServers":{"enterprise":{"command":"/usr/bin/true"}}}`
			}
			for path, data := range files {
				os.MkdirAll(filepath.Dir(path), 0700)
				if e = os.WriteFile(path, []byte(data), 0600); e != nil {
					t.Fatal(e)
				}
			}
			o.Environment["HOME"], o.Environment["USERPROFILE"], o.Environment["CLAUDE_CONFIG_DIR"] = home, home, config
			o.Environment["HTTPS_PROXY"], o.Environment["NODE_EXTRA_CA_CERTS"], o.Environment["NO_PROXY"] = proxy, ca, "localhost,127.0.0.1"
			o.Environment["CC_HAHA_DISABLE_TERMINAL_SHELL_ENV"] = "1"
			service, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: cwd, Arguments: o.Arguments, Version: "fixture", RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
				return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: managedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			}})
			if e != nil {
				t.Fatal(e)
			}
			defer service.Close(context.Background())
			host := httptest.NewServer(service)
			defer host.Close()
			query := "?cwd=" + url.QueryEscape(cwd)
			steps := []map[string]string{{"method": "GET", "path": "/api/mcp" + query}, {"method": "GET", "path": "/api/mcp" + query}}
			switch profile {
			case "cross-cwd":
				steps = append(steps, map[string]string{"method": "GET", "path": "/api/mcp?cwd=" + url.QueryEscape(other)}, map[string]string{"method": "GET", "path": "/api/mcp" + query})
			case "toggle":
				name := url.PathEscape("claude.ai alpha")
				steps = append(steps, map[string]string{"method": "POST", "path": "/api/mcp/" + name + "/toggle" + query, "body": "{}"}, map[string]string{"method": "GET", "path": "/api/mcp/" + name + "/status" + query}, map[string]string{"method": "GET", "path": "/api/mcp" + query}, map[string]string{"method": "POST", "path": "/api/mcp/" + name + "/toggle" + query, "body": "{}"}, map[string]string{"method": "GET", "path": "/api/mcp" + query})
			case "encoded-names":
				for _, name := range []string{"claude.ai 100%20 literal", "claude.ai éclair", "claude.ai alpha"} {
					steps = append(steps, map[string]string{"method": "GET", "path": "/api/mcp/" + url.PathEscape(name) + "/status" + query})
				}
				steps = append(steps, map[string]string{"method": "GET", "path": "/api/mcp/%ED%A0%80/status" + query})
			}
			actual := []any{}
			var actualRaw []json.RawMessage
			for _, step := range steps {
				req, _ := http.NewRequest(step["method"], host.URL+step["path"], strings.NewReader(step["body"]))
				req.Header.Set("Content-Type", "application/json")
				response, e := host.Client().Do(req)
				if e != nil {
					t.Fatal(e)
				}
				data, _ := io.ReadAll(response.Body)
				response.Body.Close()
				var body any
				if json.Unmarshal(data, &body) != nil {
					t.Fatal(string(data))
				}
				actual = append(actual, map[string]any{"status": float64(response.StatusCode), "body": body})
				actualRaw = append(actualRaw, raw(map[string]any{"status": response.StatusCode, "body": json.RawMessage(data)}))
			}
			if e = service.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
			goCatalog, goInit := catalogs.Swap(0), initializes.Swap(0)
			source, _ := filepath.Abs("../../testdata/mcp-api-claudeai.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", source)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_CLAUDEAI_API_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "HTTPS_PROXY=" + proxy, "NODE_EXTRA_CA_CERTS=" + ca, "NO_PROXY=127.0.0.1,localhost", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
			cmd.Stdin = bytes.NewReader(raw(map[string]any{"cwd": cwd, "files": files, "steps": steps, "managedRoot": managedRoot}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			expectedRaw, e := cmd.Output()
			if e != nil {
				t.Fatal(e, stderr.String())
			}
			var expected []any
			if json.Unmarshal(expectedRaw, &expected) != nil {
				t.Fatal(string(expectedRaw))
			}
			if !reflect.DeepEqual(actual, expected) || !reflect.DeepEqual(mcpAPISemanticJSON(t, raw(actualRaw)), mcpAPISemanticJSON(t, expectedRaw)) {
				t.Fatalf("profile=%s actual Source %s\nGo %s", profile, expectedRaw, raw(actual))
			}
			expectedCatalogs := int32(1)
			if profile == "enterprise" {
				expectedCatalogs = 0
			}
			if goCatalog != expectedCatalogs || catalogs.Load() != expectedCatalogs || goInit != initializes.Load() {
				t.Fatal("memo/probe physical count", goCatalog, catalogs.Load(), goInit, initializes.Load())
			}
			if saved, _ := os.ReadFile(filepath.Join(config, ".credentials.json")); string(saved) != credential {
				t.Fatal("account credentials overwritten")
			}
		})
	}
}

func newClaudeAIAPIOwnedFactory(t *testing.T, proxy, ca string, globals ...string) (*server.Server, *httptest.Server, string) {
	t.Helper()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, e := filepath.EvalSymlinks(o.Environment["HOME"])
	if e != nil {
		t.Fatal(e)
	}
	config, cwd := filepath.Join(home, ".claude"), filepath.Join(home, "work")
	os.MkdirAll(config, 0700)
	os.MkdirAll(cwd, 0700)
	os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{}`), 0600)
	if len(globals) > 0 {
		os.WriteFile(filepath.Join(config, ".claude.json"), []byte(globals[0]), 0600)
	}
	os.WriteFile(filepath.Join(config, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"fake-private-api-token","refreshToken":"fake-private-refresh","expiresAt":9999999999999,"scopes":["user:inference","user:mcp_servers"]}}`), 0600)
	o.Environment["HOME"], o.Environment["USERPROFILE"], o.Environment["CLAUDE_CONFIG_DIR"] = home, home, config
	o.Environment["HTTPS_PROXY"], o.Environment["NODE_EXTRA_CA_CERTS"], o.Environment["NO_PROXY"] = proxy, ca, "localhost,127.0.0.1"
	o.Environment["CC_HAHA_DISABLE_TERMINAL_SHELL_ENV"] = "1"
	value, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: cwd, Arguments: o.Arguments, Version: "fixture", RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { value.Close(context.Background()) })
	host := httptest.NewServer(value)
	t.Cleanup(host.Close)
	return value, host, cwd
}
func TestDesktopClaudeAIMCPDisconnectedWaiterRetainsSingleCatalogUntilServerClose(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	proxy, ca := claudeAIAPIPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/mcp_servers" {
			t.Errorf("unexpected catalog-only method %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		requests.Add(1)
		close(started)
		defer close(finished)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"a","display_name":"alpha","url":"https://vendor.invalid/a"}]}`)
	}))
	service, host, cwd := newClaudeAIAPIOwnedFactory(t, proxy, ca)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", host.URL+"/api/mcp?cwd="+url.QueryEscape(cwd), nil)
	clientDone := make(chan error, 1)
	go func() {
		response, e := host.Client().Do(req)
		if response != nil {
			response.Body.Close()
		}
		clientDone <- e
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("actual catalog wasn't admitted")
	}
	cancel()
	if e := <-clientDone; !errors.Is(e, context.Canceled) {
		t.Fatal("HTTP waiter disconnect", e)
	}
	select {
	case <-finished:
		t.Fatal("renderer disconnect canceled original shared catalog")
	default:
	}
	close(release)
	req, _ = http.NewRequest("GET", host.URL+"/api/mcp?cwd="+url.QueryEscape(cwd), nil)
	response, e := host.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !bytes.Contains(data, []byte(`claude.ai alpha`)) || requests.Load() != 1 {
		t.Fatal("same memo not retained", requests.Load(), string(data))
	}
	if e = service.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestDesktopClaudeAIMCPServerCloseCancelsAndJoinsHeldCatalogBeforeOwnedNativeRelease(t *testing.T) {
	started, finished := make(chan struct{}), make(chan struct{})
	proxy, ca := claudeAIAPIPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/mcp_servers" {
			w.WriteHeader(404)
			return
		}
		close(started)
		<-r.Context().Done()
		close(finished)
	}))
	service, host, cwd := newClaudeAIAPIOwnedFactory(t, proxy, ca)
	done := make(chan error, 1)
	go func() {
		response, e := host.Client().Get(host.URL + "/api/mcp?cwd=" + url.QueryEscape(cwd))
		if response != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		done <- e
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("held physical catalog not admitted")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close(context.Background()) }()
	select {
	case e := <-closeDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Server.Close deadlocked catalog API/credential owner")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("physical catalog HTTP survived actual Close")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("API operation outlived Close")
	}
	if e := service.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}

// Retain JS UTF-16 string identity during response comparison; encoding/json's
// replacement of lone surrogates must not hide a Source/API difference.
func mcpAPISemanticJSON(t *testing.T, data json.RawMessage) any {
	t.Helper()
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		t.Fatal("empty JSON")
	}
	switch data[0] {
	case '"':
		units, e := protocol.DecodeJavaScriptJSONString(data)
		if e != nil {
			t.Fatal(e)
		}
		return units
	case '{':
		var value protocol.JSONObject
		if e := value.UnmarshalJSON(data); e != nil {
			t.Fatal(e)
		}
		out := map[string]any{}
		for _, field := range value.OwnProperties() {
			units, e := protocol.DecodeJavaScriptJSONString(field.NameJSON)
			if e != nil {
				t.Fatal(e)
			}
			out[fmt.Sprint(units)] = mcpAPISemanticJSON(t, field.Value)
		}
		return out
	case '[':
		var rows []json.RawMessage
		if e := json.Unmarshal(data, &rows); e != nil {
			t.Fatal(e)
		}
		out := []any{}
		for _, row := range rows {
			out = append(out, mcpAPISemanticJSON(t, row))
		}
		return out
	default:
		var value any
		if e := json.Unmarshal(data, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
}

func TestDesktopClaudeAIMCPServerCloseJoinsActualHeldStdioChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix physical shell fixture; portable production build is checked separately")
	}
	marker := filepath.Join(t.TempDir(), "actual-mcp-child")
	globals := string(raw(map[string]any{"mcpServers": map[string]any{"held": map[string]any{"command": "/bin/sh", "args": []string{"-c", `printf '%s' "$$" > "$1"; exec /bin/sleep 30`, "owned-mcp-child", marker}}}}))
	service, host, cwd := newClaudeAIAPIOwnedFactory(t, "", "", globals)
	done := make(chan error, 1)
	go func() {
		response, e := host.Client().Get(host.URL + "/api/mcp/held/status?cwd=" + url.QueryEscape(cwd))
		if response != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		done <- e
	}()
	limit := time.NewTimer(5 * time.Second)
	defer limit.Stop()
	poll := time.NewTicker(2 * time.Millisecond)
	defer poll.Stop()
	var pid int
	for pid == 0 {
		select {
		case <-limit.C:
			t.Fatal("actual MCP stdio child did not start")
		case <-poll.C:
			bytes, e := os.ReadFile(marker)
			if e == nil {
				pid, _ = strconv.Atoi(string(bytes))
			}
		}
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close(context.Background()) }()
	select {
	case e := <-closeDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Server.Close did not join stdio MCP transport")
	}
	process, findErr := os.FindProcess(pid)
	if findErr != nil {
		t.Fatal(findErr)
	}
	if e := process.Signal(syscall.Signal(0)); !errors.Is(e, syscall.ESRCH) && !errors.Is(e, os.ErrProcessDone) {
		t.Fatal("actual MCP child survived Server.Close", pid, e)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("MCP status business operation outlived native release")
	}
}
