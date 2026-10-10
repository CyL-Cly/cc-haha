package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicGoCLIAdapterDefaultSlackFactoryActualSocketReplyAndClose(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "SLACK_APP_TOKEN", "SLACK_BOT_TOKEN"} {
		t.Setenv(key, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("ADAPTER_SERVER_URL", "ws://127.0.0.1:1")
	t.Setenv("ADAPTER_DEFAULT_PROJECT_DIR", home)
	t.Setenv("CALLER_DIR", home)
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	seed := []byte(`{"slack":{"botToken":"private-cli-bot","appToken":"private-cli-app","allowedUsers":["U1"]},"future":{"keep":true}}`)
	_ = os.WriteFile(filepath.Join(config, "adapters.json"), seed, 0600)
	ack := make(chan struct{})
	closed := make(chan struct{})
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer func() { conn.Close(); close(closed) }()
		_ = websocket.JSON.Send(conn, map[string]any{"type": "events_api", "envelope_id": "private-first", "payload": map[string]any{"event": map[string]any{"type": "message", "channel_type": "im", "channel": "D1", "user": "stranger", "text": "unpaired", "client_msg_id": "private-incoming"}}})
		for {
			var m protocol.JSONObject
			if websocket.JSON.Receive(conn, &m) != nil {
				return
			}
			if m.String("envelope_id") == "private-first" {
				close(ack)
			}
		}
	}}
	socketPeer := httptest.NewTLSServer(ws)
	defer socketPeer.Close()
	ca := filepath.Join(home, "private-ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: socketPeer.Certificate().Raw}), 0600)
	t.Setenv("NODE_EXTRA_CA_CERTS", ca)
	reply := make(chan struct{})
	var once sync.Once
	apiPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m protocol.JSONObject
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&m)
		}
		v := map[string]any{"ok": true}
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case "auth.test":
			v["user_id"] = "UBOT"
		case "apps.connections.open":
			if r.Header.Get("Authorization") != "Bearer private-cli-app" {
				t.Error("actual app token lost")
			}
			v["url"] = "wss" + strings.TrimPrefix(socketPeer.URL, "https")
		case "users.info":
			v["user"] = map[string]string{"real_name": "Private"}
		case "chat.postMessage":
			if m.String("text") != "未授权。请在 Claude Code 桌面端生成配对码后发送给我。" {
				t.Error("original gate did not run")
			}
			v["ts"] = "1.1"
			once.Do(func() { close(reply) })
		default:
			t.Error("unexpected actual API", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(v)
	}))
	defer apiPeer.Close()
	previous := newNativeSlackAPI
	newNativeSlackAPI = func(o imruntime.SlackAPIOptions) (*imruntime.SlackAPI, error) {
		if o.Token != "private-cli-bot" {
			t.Error("file token lost")
		}
		o.BaseURL = apiPeer.URL + "/"
		o.HTTP = apiPeer.Client()
		return imruntime.NewSlackAPI(o)
	}
	defer func() { newNativeSlackAPI = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan int, 1)
	go func() {
		finished <- run(ctx, []string{"adapters", "--app-root", "/private/fixture/app.asar", "--slack"})
	}()
	select {
	case <-reply:
	case <-time.After(5 * time.Second):
		t.Fatal("public default Slack did not reply")
	}
	select {
	case <-ack:
	case <-time.After(time.Second):
		t.Fatal("actual public CLI did not ACK")
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("public Slack did not Close and join")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("actual Slack socket alive after Close")
	}
	stored, _ := os.ReadFile(filepath.Join(config, "adapters.json"))
	if string(stored) != string(seed) {
		t.Fatal("CLI mutated legacy config")
	}
}
