package petaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualSourcePetHTTPMessagesAndUTF16Projection(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type request struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
	}
	httpRows := []request{}
	for _, path := range []string{"/health", "/api/sessions", "/api/sessions/sid_1/chat/status", "/api/sessions/%73id_1/chat/status", "/api/sessions/" + strings.Repeat("a", 65) + "/chat/status", "/api/sessions/sid/history", "/ws/sid", "/ws/sid/", "/sdk/sid", "/api/desktop-ui/preferences/pet", "/api/desktop-ui/preferences/general", "/api/providers"} {
		for _, method := range []string{"GET", "PUT", "POST", "OPTIONS"} {
			httpRows = append(httpRows, request{"http://127.0.0.1" + path, method, map[string]string{"Access-Control-Request-Method": "get"}})
		}
	}
	clients := []json.RawMessage{[]byte(`null`), []byte(`[]`), []byte(`{}`), []byte(`{"type":"ping"}`), []byte(`{"type":"ping","extra":null}`), []byte(`{"type":"sync_state"}`), []byte(`{"type":"stop_generation"}`), []byte(`{"type":"permission_response"}`), []byte(`{"type":"user_message","content":null}`), []byte(`{"type":"user_message","content":""}`), []byte(`{"type":"user_message","content":"\ufeff \t"}`), []byte(`{"type":"user_message","content":"\ufeff /hello"}`), []byte(`{"type":"user_message","content":"\u0085/hello"}`), []byte(`{"type":"user_message","content":"\ud800"}`), []byte(`{"type":"user_message","content":"hello","attachments":[]}`)}
	for _, n := range []int{16383, 16384, 16385} {
		b, _ := json.Marshal(map[string]any{"type": "user_message", "content": strings.Repeat("😀", n)})
		clients = append(clients, b)
	}
	servers := []json.RawMessage{}
	for _, kind := range []string{"connected", "session_state", "pong", "permission_requests_snapshot", "status", "error", "message_complete", "tool_result", "content_delta", "permission_request", "session_title_updated"} {
		b, _ := json.Marshal(map[string]any{"type": kind, "state": "running", "sessionId": "sid", "toolRequestIds": []string{"secret"}, "computerUseRequestIds": []string{"secret"}, "code": "FAIL", "retryable": false, "businessErrorCode": "private-code", "usage": map[string]int{"input_tokens": 123}, "message": "private details", "extra": "unknown"})
		servers = append(servers, b)
	}
	servers = append(servers, []byte(`{"type":"error"}`), []byte(`{"type":"status"}`))
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-pet-policy.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "http": httpRows, "client": clients, "server": servers})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PET_POLICY_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %s", e, stderr.String())
	}
	var source struct {
		HTTP []struct {
			Allowed   bool
			SessionID *string
		}
		Client []bool
		Server []json.RawMessage
	}
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	if len(source.HTTP) != len(httpRows) || len(source.Client) != len(clients) || len(source.Server) != len(servers) {
		t.Fatal("Source shape")
	}
	for i, r := range httpRows {
		u, _ := url.Parse(r.URL)
		if got := HTTPAllowed(r.Method, u.EscapedPath(), r.Headers["Access-Control-Request-Method"]); got != source.HTTP[i].Allowed {
			t.Errorf("http%d Go%v Source%v", i, got, source.HTTP[i].Allowed)
		}
		id := ScopedSessionID(u.EscapedPath())
		if (source.HTTP[i].SessionID == nil && id != "") || (source.HTTP[i].SessionID != nil && id != *source.HTTP[i].SessionID) {
			t.Errorf("scoped%d", i)
		}
	}
	for i, r := range clients {
		if got := ClientMessageAllowed(r); got != source.Client[i] {
			t.Errorf("client%d %s Go%v Source%v", i, r, got, source.Client[i])
		}
	}
	for i, r := range servers {
		got := ServerMessage(r)
		var a, b any
		if got != nil {
			_ = json.Unmarshal(got, &a)
		}
		_ = json.Unmarshal(source.Server[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("server%d Go%s Source%s", i, got, source.Server[i])
		}
	}
}
