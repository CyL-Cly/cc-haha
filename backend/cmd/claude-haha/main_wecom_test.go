package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

func TestPublicGoCLIAdapterDefaultWecomFactoryActualAuthInboundReplyAndClose(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "WECOM_BOT_ID", "WECOM_BOT_SECRET", "NODE_EXTRA_CA_CERTS"} {
		t.Setenv(key, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("ADAPTER_SERVER_URL", "ws://127.0.0.1:1")
	t.Setenv("ADAPTER_DEFAULT_PROJECT_DIR", home)
	t.Setenv("CALLER_DIR", home)
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	seed := []byte(`{"wecom":{"botId":"private-bot","secret":"private-secret","allowedUsers":[]},"future":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), seed, 0600); err != nil {
		t.Fatal(err)
	}
	replied, closed := make(chan struct{}), make(chan struct{})
	var replyOnce, closeOnce sync.Once
	peer := httptest.NewTLSServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer func() { conn.Close(); closeOnce.Do(func() { close(closed) }) }()
		for {
			var frame protocol.JSONObject
			if websocket.JSON.Receive(conn, &frame) != nil {
				return
			}
			ack := protocol.NewJSONObject(protocol.JSONField{Name: "headers", Value: frame.Get("headers")}, protocol.JSONField{Name: "errcode", Value: []byte(`0`)}, protocol.JSONField{Name: "body", Value: []byte(`{}`)})
			if err := websocket.JSON.Send(conn, ack); err != nil {
				return
			}
			switch frame.String("cmd") {
			case "aibot_subscribe":
				var body protocol.JSONObject
				_ = body.UnmarshalJSON(frame.Get("body"))
				if body.String("bot_id") != "private-bot" || body.String("secret") != "private-secret" {
					t.Error("raw file credentials lost")
				}
				_ = websocket.Message.Send(conn, `{"cmd":"aibot_msg_callback","headers":{"req_id":"private-incoming"},"body":{"msgtype":"text","from":{"userid":"stranger"},"msgid":"private-first","text":{"content":"hello"}}}`)
			case "aibot_send_msg":
				var body, markdown protocol.JSONObject
				_ = body.UnmarshalJSON(frame.Get("body"))
				_ = markdown.UnmarshalJSON(body.Get("markdown"))
				if markdown.String("content") != "未授权。请在 Claude Code 桌面端生成配对码后发送给我。" {
					t.Error("original pairing gate did not run")
				}
				replyOnce.Do(func() { close(replied) })
			}
		}
	}})
	defer peer.Close()
	previous := newNativeWecomClient
	newNativeWecomClient = func(ctx context.Context, options imruntime.WecomClientOptions) (*imruntime.WecomClient, error) {
		options.Dial = func(ctx context.Context, address string, _ *tls.Config) (*imruntime.Socket, error) {
			if address != imruntime.WecomWebSocketURL {
				t.Error("default official URL changed")
			}
			transport := peer.Client().Transport.(*http.Transport)
			return imruntime.DialSocket(ctx, "wss"+strings.TrimPrefix(peer.URL, "https"), transport.TLSClientConfig)
		}
		return imruntime.NewWecomClient(ctx, options)
	}
	defer func() { newNativeWecomClient = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan int, 1)
	go func() {
		finished <- run(ctx, []string{"adapters", "--app-root", "/private/fixture/app.asar", "--wecom"})
	}()
	select {
	case <-replied:
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary Wecom factory did not reach original reply")
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("public Wecom did not Close and join")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("actual Wecom socket survived Close")
	}
	stored, err := os.ReadFile(filepath.Join(config, "adapters.json"))
	if err != nil || string(stored) != string(seed) {
		t.Fatal("ordinary startup mutated stored fields", err)
	}
}
