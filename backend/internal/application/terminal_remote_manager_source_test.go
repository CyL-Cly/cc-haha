package application

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestActualSourceRemoteSessionManagerWebSocketHTTPPermissionInterruptAndPermanentClose(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{".credentials.json": `{"claudeAiOauth":{"accessToken":"owned-manager-token","expiresAt":9999999999999,"subscriptionType":"max","scopes":["user:inference","user:profile"]}}`, ".claude-local-oauth.json": `{"oauthAccount":{"organizationUuid":"owned-org"}}`} {
		if err := os.WriteFile(filepath.Join(config, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var socket *websocket.Conn
	var controls []json.RawMessage
	var posted json.RawMessage
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owned-manager-token" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("actual Source credential headers", r.Header)
		}
		switch r.URL.Path {
		case "/v1/sessions/ws/session_owned_manager/subscribe":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer c.CloseNow()
			mu.Lock()
			socket = c
			mu.Unlock()
			_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"future_sdk","actual":true}`))
			for {
				_, data, err := c.Read(r.Context())
				if err != nil {
					return
				}
				mu.Lock()
				controls = append(controls, append(json.RawMessage(nil), data...))
				count := len(controls)
				mu.Unlock()
				if count == 3 {
					_ = c.Close(websocket.StatusCode(4003), "owned permanent rejection")
					return
				}
			}
		case "/v1/sessions/session_owned_manager/events":
			data, _ := io.ReadAll(r.Body)
			mu.Lock()
			posted = append(json.RawMessage(nil), data...)
			c := socket
			mu.Unlock()
			if r.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" || r.Header.Get("x-organization-uuid") != "owned-org" || !strings.Contains(string(data), "OWNED_MANAGER_HUMAN") {
				t.Error(r.Header, string(data))
			}
			if c == nil {
				t.Error("actual Source event preceded its real connected callback")
				w.WriteHeader(500)
				return
			}
			_, _ = io.WriteString(w, `{}`)
			_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"control_request","request_id":"unsupported-control","request":{"subtype":"future_control"}}`))
			_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"control_request","request_id":"owned-permission","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"owned-tool","input":{"command":"private-remote-command"},"description":"owned permission"}}`))
			_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"control_cancel_request","request_id":"unknown-pending"}`))
		default:
			t.Error("unowned Source manager request", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer peer.Close()
	ca := filepath.Join(home, "private-peer-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-remote-session-manager.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=ant", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USE_LOCAL_OAUTH=1", "CLAUDE_LOCAL_OAUTH_API_BASE=" + peer.URL, "NODE_EXTRA_CA_CERTS=" + ca, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_INTERNAL_FC_OVERRIDES={\"tengu_remote_backend\":true}", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REMOTE_MANAGER_FIXTURE=1"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "peer": peer.URL}))
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, string(output), diagnostics.String())
	}
	var result struct {
		Gate        bool
		Incoming    []json.RawMessage
		States      []string
		Connections []json.RawMessage
		HTTP        []json.RawMessage
		Caps        struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err, string(output))
	}
	if !result.Gate || len(result.Caps.Blocked) != 0 || len(result.Connections) != 1 || len(result.HTTP) != 1 || len(result.Incoming) != 1 || obj(result.Incoming[0]).String("type") != "future_sdk" || strings.Join(result.States, ",") != "connected,disconnected" {
		t.Fatal(string(output))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(controls) != 3 || !strings.Contains(string(posted), `"uuid":"owned-manager-human"`) {
		t.Fatal(string(posted), controls)
	}
	if obj(obj(controls[0]).Get("response")).String("error") != "Unsupported control request subtype: future_control" || obj(obj(obj(controls[1]).Get("response")).Get("response")).String("behavior") != "allow" || obj(obj(controls[2]).Get("request")).String("subtype") != "interrupt" {
		t.Fatal(controls)
	}
	t.Log("actual full RemoteSessionManager + SessionsWebSocket, trusted private TLS, physical HTTP and three wire controls, zero forbidden capabilities")
}
