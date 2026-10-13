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
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestFrozenPrivateH5PublicColdInspectionSnapshot(t *testing.T) {
	historyPath, configPath := os.Getenv("CC_HAHA_H5_BEFORE_HISTORY"), os.Getenv("CC_HAHA_H5_BEFORE_CONFIG")
	if historyPath == "" || configPath == "" {
		t.Skip("explicit credential-free frozen snapshot required")
	}
	history, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var frozen map[string]json.RawMessage
	if err = json.Unmarshal(config, &frozen); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected inference", 500) }))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	// A desktop configured through its saved provider has no direct credential
	// in the process environment before an actual Agent is activated.
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"} {
		delete(o.Environment, key)
		delete(o.Native.Environment, key)
	}
	const id = "93907af7-084f-4d9c-a943-5c6c25780a20"
	project := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.WorkDir))
	if err = os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	var rows []byte
	for _, line := range bytes.Split(history, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		row := obj(line)
		for _, key := range []string{"cwd", "workDir"} {
			if row.Get(key) != nil {
				row.Set(key, raw(o.WorkDir))
			}
		}
		encoded, err := row.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, append(encoded, '\n')...)
	}
	path := filepath.Join(project, id+".jsonl")
	if err = os.WriteFile(path, rows, 0600); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha")
	if err = os.MkdirAll(managed, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(managed, "providers.json"), frozen["providers"], 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), frozen["settings"], 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	res, err := http.Get(host.URL + "/api/sessions/" + id + "/inspection?includeContext=1&contextOnly=1")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 200 {
		t.Fatal("frozen public cold inspection", res.StatusCode, err, string(data))
	}
	if obj(obj(data).Get("contextEstimate")).Get("totalTokens") == nil {
		t.Fatal("context footer estimate missing")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(rows, after) || calls.Load() != 0 {
		t.Fatal("readonly inspection mutated/inferred", err, calls.Load())
	}
}
