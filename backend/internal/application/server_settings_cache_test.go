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
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopSettingsWriteInvalidatesCompleteNativePolicyWithoutStartingAgent(t *testing.T) {
	var modelCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		modelCalls.Add(1)
		http.Error(w, "settings routes must not start the Agent", 500)
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"cleanupPeriodDays":0,"respectGitignore":true,"outputStyle":"Default","future":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{".gitignore": "secret.txt\n", "secret.txt": "private fixture"} {
		if err := os.WriteFile(filepath.Join(o.WorkDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(method, path string, body any, status int) []byte {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, host.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := host.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		result, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != status {
			t.Fatalf("%s %s: status=%d body=%s err=%v", method, path, resp.StatusCode, result, err)
		}
		return result
	}
	create := func() string {
		t.Helper()
		var result struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(request("POST", "/api/sessions", map[string]string{"workDir": o.WorkDir}, 201), &result); err != nil || result.SessionID == "" {
			t.Fatal("create session", result, err)
		}
		return result.SessionID
	}
	search := func(want int) {
		t.Helper()
		var result struct {
			Entries []json.RawMessage `json:"entries"`
		}
		data := request("GET", "/api/filesystem/browse?path="+url.QueryEscape(o.WorkDir)+"&includeFiles=true&search=secret", nil, 200)
		if err := json.Unmarshal(data, &result); err != nil || len(result.Entries) != want {
			t.Fatalf("respectGitignore stale: want=%d body=%s err=%v", want, data, err)
		}
	}
	// These three consumers warm one complete Source settings cache.
	request("GET", "/api/settings/output-styles", nil, 200)
	memoryID := create()
	search(0)
	projects := filepath.Join(config, "projects")
	if _, err := os.Stat(projects); !os.IsNotExist(err) {
		t.Fatalf("retention zero created project storage: %v", err)
	}
	request("PUT", "/api/settings/user", map[string]any{"cleanupPeriodDays": 30, "respectGitignore": false, "outputStyle": "Learning"}, 200)
	persistedID := create()
	search(1)
	styles := request("GET", "/api/settings/output-styles", nil, 200)
	if !bytes.Contains(styles, []byte(`"outputStyle":"Learning"`)) {
		t.Fatalf("style cache stale after HTTP write: %s", styles)
	}
	var transcriptPaths []string
	if err := filepath.WalkDir(projects, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".jsonl" {
			transcriptPaths = append(transcriptPaths, path)
		}
		return nil
	}); err != nil || len(transcriptPaths) != 1 || filepath.Base(transcriptPaths[0]) != persistedID+".jsonl" {
		t.Fatalf("positive retention not observed: %v %v", transcriptPaths, err)
	}
	request("GET", "/api/sessions/"+memoryID+"/summary", nil, 404)
	page := request("GET", "/api/sessions/"+memoryID+"/messages?mode=page", nil, 200)
	if !bytes.Contains(page, []byte(`"sourceVersion":"memory"`)) {
		t.Fatalf("changing policy changed existing memory session: %s", page)
	}
	request("PUT", "/api/settings/user", map[string]any{"cleanupPeriodDays": 0, "respectGitignore": true}, 200)
	lastMemoryID := create()
	search(0)
	if _, err := os.Stat(filepath.Join(filepath.Dir(transcriptPaths[0]), lastMemoryID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("retention zero ignored after HTTP write: %v", err)
	}
	persisted, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || !bytes.Contains(persisted, []byte(`"keep": true`)) || !bytes.Contains(persisted, []byte(`"outputStyle": "Learning"`)) {
		t.Fatalf("settings write lost unknown or untouched fields: %s %v", persisted, err)
	}
	if calls := modelCalls.Load(); calls != 0 {
		t.Fatalf("settings APIs started model requests: %d", calls)
	}
}
