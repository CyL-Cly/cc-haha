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

func TestDesktopAgentsFactoryUsesSharedDefinitionsAndRealScopedMutations(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	config, home := o.Environment["CLAUDE_CONFIG_DIR"], o.Environment["HOME"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"future":{"preserved":true}}`), 0600); err != nil {
		t.Fatal(err)
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
			t.Fatalf("%s %s %d: %s %v", method, path, res.StatusCode, data, err)
		}
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(string(data), err)
		}
		return value
	}
	initial := request("GET", "/api/agents", "", 200)
	if len(initial["activeAgents"].([]any)) != 6 {
		t.Fatal("actual stock Agent producer", initial)
	}
	for _, tool := range initial["availableTools"].([]any) {
		if tool == "Agent" || tool == "AskUserQuestion" || tool == "TaskStop" {
			t.Fatal("Source disallowed async custom tool advertised", tool)
		}
	}
	created := request("POST", "/api/agents", `{"scope":"user","name":"api-worker","description":"Actual factory reviewer","model":"deepseek-flash","tools":["Read"],"systemPrompt":"Review only the supplied task."}`, 201)
	if created["agent"].(map[string]any)["source"] != "userSettings" {
		t.Fatal(created)
	}
	path := filepath.Join(config, "agents", "api-worker.md")
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("Review only the supplied task.")) {
		t.Fatal(string(data), err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("Agent file ownership", info, err)
	}
	request("GET", "/api/agents/api-worker", "", 200)
	request("PUT", "/api/agents/api-worker", `{"scope":"user","description":"Updated factory reviewer","systemPrompt":"Updated actual task."}`, 200)
	data, err = os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("Updated actual task.")) {
		t.Fatal(string(data), err)
	}
	request("POST", "/api/agents", `{"scope":"project","name":"api-worker","description":"Project wins","tools":["Read"],"systemPrompt":"Project definition."}`, 201)
	detail := request("GET", "/api/agents/api-worker", "", 200)["agent"].(map[string]any)
	if detail["source"] != "projectSettings" {
		t.Fatal("real project override not effective", detail)
	}
	request("PUT", "/api/agents/Explore/override", `{"model":"deepseek-flash","effort":"high"}`, 200)
	settings, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || !bytes.Contains(settings, []byte(`"preserved": true`)) && !bytes.Contains(settings, []byte(`"preserved":true`)) || !bytes.Contains(settings, []byte("deepseek-flash")) {
		t.Fatal(string(settings), err)
	}
	request("DELETE", "/api/agents/Explore/override", "", 200)
	reload := request("POST", "/api/agents/reload?sessionId=not-running", "", 200)["session"].(map[string]any)
	if reload["applied"] != false || reload["reason"] != "not_running" {
		t.Fatal("disk mutation falsely reported applied", reload)
	}
	request("PUT", "/api/agents/general-purpose", `{"scope":"user","description":"forbidden"}`, 403)
	request("GET", "/api/agents/unknown", "", 404)
	request("POST", "/api/agents", `{"scope":"user","name":"bad/name","description":"invalid"}`, 400)
	request("DELETE", "/api/agents/api-worker?scope=project", "", 200)
	detail = request("GET", "/api/agents/api-worker", "", 200)["agent"].(map[string]any)
	if detail["source"] != "userSettings" {
		t.Fatal("removing override did not reveal user definition", detail)
	}
	request("DELETE", "/api/agents/api-worker?scope=user", "", 200)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("private Agent file not deleted", err)
	}
	final := request("GET", "/api/agents", "", 200)
	if len(final["activeAgents"].([]any)) != 6 || strings.Contains(string(raw(final)), "api-worker") {
		t.Fatal("shared definition cache remains stale", final)
	}
}
