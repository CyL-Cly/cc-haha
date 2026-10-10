package application

import (
	"context"
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

func TestDesktopConstructorInspectionAndSlashCommandsReadPhysicalOldSessionWithoutInference(t *testing.T) {
	var models atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		models.Add(1)
		http.Error(w, "read-only inspection must not infer", 500)
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Environment["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"] = `{"fixture-model":1000000}`
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	project := filepath.Join(config, "projects", "private")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	content := append(raw(map[string]any{"type": "session-meta", "workDir": o.WorkDir, "permissionMode": "plan", "runtimeProviderId": nil}), '\n')
	content = append(content, append(raw(map[string]any{"type": "assistant", "uuid": "a1", "timestamp": "2026-10-07T00:00:00Z", "message": map[string]any{"id": "api1", "model": "fixture-model", "role": "assistant", "content": "original real-format response", "usage": map[string]any{"input_tokens": 12, "output_tokens": 8}}}), '\n')...)
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), content, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	get := func(path string) protocol.JSONObject {
		t.Helper()
		res, err := host.Client().Get(host.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 {
			t.Fatal(path, res.StatusCode, string(data), err)
		}
		return obj(data)
	}
	inspection := get("/api/sessions/" + id + "/inspection?contextOnly=1")
	status := obj(inspection.Get("status"))
	usage := obj(inspection.Get("usage"))
	estimate := obj(inspection.Get("contextEstimate"))
	if string(inspection.Get("active")) != "false" || status.String("permissionMode") != "plan" || status.String("model") != "fixture-model" || usage.String("source") != "transcript" || string(usage.Get("totalInputTokens")) != "12" || string(usage.Get("totalOutputTokens")) != "8" || string(estimate.Get("maxTokens")) != "1000000" {
		t.Fatal("Constructor missed actual native pricing/window/metadata", inspection)
	}
	commands := get("/api/sessions/" + id + "/slash-commands")
	if len(commands.Get("commands")) == 0 || string(status.Get("slashCommandCount")) != "16" {
		t.Fatal("Constructor did not bind actual compiled cold menu", commands, status)
	}
	if models.Load() != 0 {
		t.Fatal("Read-only desktop APIs started inference", models.Load())
	}
	after, err := os.ReadFile(filepath.Join(project, id+".jsonl"))
	if err != nil || string(after) != string(content) {
		t.Fatal("Desktop inspector changed original history", err)
	}
}
