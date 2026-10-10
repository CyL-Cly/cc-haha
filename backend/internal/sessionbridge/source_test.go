package sessionbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestBridgeConfigAndPhysicalResponsesMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	configurations := []map[string]string{{}, {"CC_HAHA_DESKTOP_SERVER_URL": "http://127.0.0.1:3456"}}
	for _, url := range []string{"http://127.0.0.1:80/path?query=yes#fragment", "http://LOCALHOST:3456", "http://[::1]:3456", "http://0x7f000001:3456", "http://127.0.0.2:3456", "https://localhost:3456", "http://user:secret@localhost:3456", "http://example.com", "invalid-url"} {
		configurations = append(configurations, map[string]string{"CC_HAHA_DESKTOP_SERVER_URL": url, "CC_HAHA_SESSION_COLLABORATION_TOKEN": "fixture-secret", "CC_HAHA_SESSION_ID": "fixture-caller"})
	}
	type responseCase struct {
		Status int
		Body   string
		Action string
		Input  protocol.JSONObject
	}
	responses := []responseCase{{200, `{"status":"queued","id":"message"}`, "send", bridgeObject(`{"targetSessionId":"peer","content":"hello"}`)}, {403, `private proxy text must stay private`, "read", bridgeObject(`{}`)}, {409, `{"error":"CONFLICT","message":"Session creation requires recovery"}`, "create", bridgeObject(`{}`)}, {500, `{"error":"INTERNAL_ERROR","message":"private internals"}`, "list", bridgeObject(`{}`)}, {200, `[]`, "list", bridgeObject(`{}`)}, {200, `null`, "read", bridgeObject(`{}`)}}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(calls.Add(1)-1) % len(responses)
		row := responses[index]
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/api/session-collaboration/"+row.Action || r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("X-Session-Id") != "fixture-caller" || r.Header.Get("Origin") != "" {
			t.Error("actual bridge credentials/routing changed", r.Method, r.URL.Path)
		}
		if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(body), protocol.StringifyValidatedJavaScriptJSON(bridgeRaw(row.Input))) {
			t.Error("actual input changed", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(row.Status)
		_, _ = io.WriteString(w, row.Body)
	}))
	defer peer.Close()
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-bridge.fixture.ts")
	rows := []any{}
	for _, row := range responses {
		rows = append(rows, map[string]any{"action": row.Action, "input": row.Input})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SESSION_BRIDGE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(bridgeRaw(map[string]any{"homeDir": home, "endpoint": peer.URL, "configurations": configurations, "credentials": []any{}, "responses": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source bridge %v %.2000s", e, stderr.String())
	}
	var source struct {
		Configurations, Responses []json.RawMessage
		Capabilities              struct{ Blocked []any }
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v blocked%v %.1000s", e, source.Capabilities.Blocked, data)
	}
	for index, environment := range configurations {
		config, e := GetConfig(environment)
		actual := map[string]any{"available": e == nil && config != nil, "config": nil}
		if e != nil {
			delete(actual, "config")
			actual["error"] = e.Error()
		} else if config != nil {
			actual["config"] = map[string]any{"endpoint": config.Endpoint, "token": config.Token, "sessionId": config.SessionID}
		}
		bridgeCompare(t, index, actual, source.Configurations[index])
	}
	config, _ := GetConfig(map[string]string{"CC_HAHA_DESKTOP_SERVER_URL": peer.URL, "CC_HAHA_SESSION_COLLABORATION_TOKEN": "fixture-secret", "CC_HAHA_SESSION_ID": "fixture-caller"})
	for index, row := range responses {
		result, e := config.Call(ctx, row.Action, row.Input)
		actual := map[string]any{"result": result}
		if e != nil {
			actual = map[string]any{"error": e.Error()}
		}
		bridgeCompare(t, index, actual, source.Responses[index])
	}
	if calls.Load() != int32(2*len(responses)) {
		t.Fatal("did not compare actual native HTTP calls", calls.Load())
	}
}
func bridgeObject(input string) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON([]byte(input))
	return o
}
func bridgeRaw(value any) json.RawMessage { b, _ := json.Marshal(value); return b }
func bridgeCompare(t *testing.T, index int, actual any, expected json.RawMessage) {
	t.Helper()
	var a, b any
	_ = json.Unmarshal(bridgeRaw(actual), &a)
	_ = json.Unmarshal(expected, &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Source differs at %d Go%s Source%s", index, bridgeRaw(actual), strings.TrimSpace(string(expected)))
	}
}
