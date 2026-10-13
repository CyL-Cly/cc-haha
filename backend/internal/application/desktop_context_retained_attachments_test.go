package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

func TestActualDesktopOldFileAndPDFContextInspectionColdAndWarmIsReadOnly(t *testing.T) {
	for _, direct := range []bool{true, false} {
		name := "saved-provider-only"
		if direct {
			name = "direct-credential"
		}
		t.Run(name, func(t *testing.T) { runOldFilePDFContextInspection(t, direct) })
	}
}
func runOldFilePDFContextInspection(t *testing.T, direct bool) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) != 1 {
			t.Error("read-only inspection started additional inference")
			http.Error(w, "extra inference", http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("Original retained response")) {
			t.Error("warm actual owner lost original conversation")
		}
		response(w, "context-warm-once", "", nil, "ACTUAL_RETAINED_CONTEXT_WARMED")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Environment["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"] = `{"fixture-model":1000000}`
	if !direct {
		for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"} {
			delete(o.Environment, key)
			delete(o.Native.Environment, key)
		}
	}
	const id = "fc631041-5b41-42af-aec5-b1dc1d6df08d"
	project := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(o.WorkDir))
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}

	managedConfig := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cc-haha")
	if err := os.MkdirAll(managedConfig, 0700); err != nil {
		t.Fatal(err)
	}
	provider := map[string]any{"id": "context-private-loopback", "presetId": "deepseek", "name": "Private retained context loopback", "baseUrl": peer.URL, "apiKey": "fake-only-loopback", "authStrategy": "api_key", "apiFormat": "anthropic", "models": map[string]string{"main": "fixture-model", "haiku": "fixture-model", "sonnet": "fixture-model", "opus": "fixture-model"}, "modelContextWindows": map[string]int{"fixture-model": 1000000}}
	if err := os.WriteFile(filepath.Join(managedConfig, "providers.json"), raw(map[string]any{"schemaVersion": 6, "activeId": "context-private-loopback", "providers": []any{provider}, "providerOrder": []string{"context-private-loopback"}}), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, id+".jsonl")
	missing := filepath.Join(o.WorkDir, "retained-file-is-not-on-disk.txt")
	rows := []map[string]any{
		{"type": "session-meta", "sessionId": id, "workDir": o.WorkDir, "permissionMode": "bypassPermissions", "runtimeProviderId": "context-private-loopback"},
		{"type": "user", "uuid": "562137bd-9851-4837-8535-2dc22ce8625f", "sessionId": id, "cwd": o.WorkDir, "timestamp": "2026-10-07T00:00:00Z", "message": map[string]any{"role": "user", "content": "Original old-format retained file conversation"}},
		{"type": "assistant", "uuid": "45f33a68-9cb4-491f-9219-3ad83bc51d01", "parentUuid": "562137bd-9851-4837-8535-2dc22ce8625f", "sessionId": id, "timestamp": "2026-10-07T00:00:01Z", "message": map[string]any{"id": "actual-retained-usage", "role": "assistant", "model": "fixture-model", "content": []any{map[string]any{"type": "text", "text": "Original retained response"}}, "usage": map[string]any{"input_tokens": 120, "output_tokens": 8}}},
		{"type": "attachment", "uuid": "c68eedce-0fb3-463a-ab86-810974bd0b70", "parentUuid": "45f33a68-9cb4-491f-9219-3ad83bc51d01", "sessionId": id, "timestamp": "2026-10-07T00:00:02Z", "attachment": map[string]any{"type": "file", "filename": missing, "content": map[string]any{"type": "text", "file": map[string]any{"filePath": missing, "content": "Retained bytes 中文🙂 survive without opening the old file", "numLines": 1, "startLine": 1, "totalLines": 1}}}},
		{"type": "attachment", "uuid": "a800b115-247d-4342-b33c-8984e13f9bb7", "parentUuid": "c68eedce-0fb3-463a-ab86-810974bd0b70", "sessionId": id, "timestamp": "2026-10-07T00:00:03Z", "attachment": map[string]any{"type": "file", "filename": filepath.Join(o.WorkDir, "not-on-disk.pdf"), "content": map[string]any{"type": "pdf", "file": map[string]any{"filePath": filepath.Join(o.WorkDir, "not-on-disk.pdf"), "base64": "JVBERi0xLjQ=", "originalSize": 2048}}}},
	}
	var history []byte
	for _, row := range rows {
		history = append(history, append(raw(row), '\n')...)
	}
	if err := os.WriteFile(path, history, 0600); err != nil {
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
	client := &http.Client{Timeout: 20 * time.Second}
	get := func(warm bool) protocol.JSONObject {
		t.Helper()
		before, err := os.ReadFile(path)
		beforeCalls := calls.Load()
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Get(host.URL + "/api/sessions/" + id + "/inspection?includeContext=1&contextOnly=1")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatal("actual old transcript inspection", warm, res.StatusCode, err, string(data))
		}
		value := obj(data)
		if string(value.Get("active")) != string(raw(warm)) {
			t.Fatal("inspection did not use expected actual runtime", string(data))
		}
		contextData := obj(value.Get("context"))
		if !contextData.IsObject() {
			contextData = obj(value.Get("contextEstimate"))
		}
		if contextData.String("model") != "fixture-model" || string(contextData.Get("maxTokens")) != "1000000" || contextData.Get("totalTokens") == nil || string(contextData.Get("totalTokens")) == "0" {
			t.Fatal("footer has no same-model context payload", warm, string(data))
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) || calls.Load() != beforeCalls {
			t.Fatal("read-only context API mutated history or inferred", warm, err, calls.Load())
		}
		return value
	}
	cold := get(false)
	if usage := obj(cold.Get("usage")); usage.String("source") != "transcript" || string(usage.Get("totalInputTokens")) != "120" {
		t.Fatal("old transcript usage producer missing", string(raw(cold)))
	}
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetDeadline(time.Now().Add(20 * time.Second))
	if err = websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Continue the original retained conversation once"}))); err != nil {
		t.Fatal(err)
	}
	completed := false
	for {
		var data string
		if err = websocket.Message.Receive(ws, &data); err != nil {
			t.Fatal("actual warm runtime admission", err)
		}
		frame := obj([]byte(data))
		if frame.String("type") == "error" {
			t.Fatal(data)
		}
		if frame.String("type") == "message_complete" {
			completed = true
			requestDesktopSessionSync(t, ws)
		}
		if completed && frame.String("type") == "session_state" {
			assertDesktopIdleSnapshot(t, frame)
			break
		}
	}
	warm := get(true)
	if !obj(warm.Get("context")).IsObject() || obj(warm.Get("errors")).Get("context") != nil {
		t.Fatal("actual owner did not return live footer context", string(raw(warm)))
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("inspection opened/restored named file instead of retained data", err)
	}
}
