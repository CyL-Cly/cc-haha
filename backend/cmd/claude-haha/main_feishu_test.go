package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type feishuCLITransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (d feishuCLITransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "open.feishu.cn" {
		return d.next.RoundTrip(req)
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = d.target.Scheme, d.target.Host
	copy.URL = &u
	return d.next.RoundTrip(copy)
}
func feishuCLIObject(value json.RawMessage) protocol.JSONObject {
	o := protocol.NewJSONObject()
	_ = o.UnmarshalJSON(value)
	return o
}

func TestPublicGoCLIAdapterDefaultFeishuFactoryActualBinaryACKPairingReplyAndClose(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "FEISHU_APP_ID", "FEISHU_APP_SECRET", "FEISHU_DOMAIN", "FEISHU_ENCRYPT_KEY", "FEISHU_VERIFICATION_TOKEN", "NODE_EXTRA_CA_CERTS"} {
		t.Setenv(key, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("ADAPTER_SERVER_URL", "ws://127.0.0.1:1")
	t.Setenv("ADAPTER_DEFAULT_PROJECT_DIR", home)
	t.Setenv("CALLER_DIR", home)
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	seed := []byte(`{"feishu":{"appId":"private-id","appSecret":"private-secret","allowedUsers":[]},"future":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), seed, 0600); err != nil {
		t.Fatal(err)
	}
	replied, acked, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var replyOnce, ackOnce, closeOnce sync.Once
	var address string
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer func() { conn.Close(); closeOnce.Do(func() { close(closed) }) }()
		event := map[string]any{"schema": "2.0", "header": map[string]string{"event_type": "im.message.receive_v1"}, "event": map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": "stranger"}}, "message": map[string]any{"message_id": "private-body", "chat_id": "chat-1", "chat_type": "p2p", "message_type": "text", "content": `{"text":"hello"}`}}}
		frame := imruntime.FeishuFrame{SeqID: 1, LogID: 4, Service: 9, Method: 1, Headers: []imruntime.FeishuHeader{{Key: "type", Value: "event"}, {Key: "message_id", Value: "private-header"}, {Key: "sum", Value: "1"}, {Key: "seq", Value: "0"}}, Payload: mustFeishuCLIJSON(event)}
		conn.PayloadType = websocket.BinaryFrame
		if err := websocket.Message.Send(conn, imruntime.EncodeFeishuFrame(frame)); err != nil {
			t.Error(err)
			return
		}
		for {
			var packet []byte
			if websocket.Message.Receive(conn, &packet) != nil {
				return
			}
			ack, err := imruntime.DecodeFeishuFrame(packet)
			if err != nil {
				t.Error(err)
				return
			}
			for _, h := range ack.Headers {
				if h.Key == "message_id" && h.Value == "private-header" {
					var v map[string]any
					_ = json.Unmarshal(ack.Payload, &v)
					if v["code"] != float64(200) {
						t.Error("original ACK changed")
					}
					ackOnce.Do(func() { close(acked) })
				}
			}
		}
	}}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/stream" {
			ws.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var body protocol.JSONObject
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && err != io.EOF {
			t.Error(err)
			return
		}
		switch req.URL.Path {
		case "/callback/ws/endpoint":
			if body.String("AppID") != "private-id" || body.String("AppSecret") != "private-secret" {
				t.Error("raw file credentials lost")
			}
			_, _ = w.Write(mustFeishuCLIJSON(map[string]any{"code": 0, "data": map[string]any{"URL": "wss" + strings.TrimPrefix(address, "https") + "/stream?device_id=private&service_id=9", "ClientConfig": map[string]any{"PingInterval": 1000, "ReconnectCount": 0, "ReconnectInterval": 1000, "ReconnectNonce": 0}}}))
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"tenant_access_token":"private-access","expire":3600}`))
		case "/open-apis/bot/v3/info":
			_, _ = w.Write([]byte(`{"code":0,"bot":{"open_id":"private-bot"}}`))
		case "/open-apis/im/v1/messages":
			if req.Header.Get("Authorization") != "Bearer private-access" {
				t.Error("actual tenant token lost")
			}
			content := body.String("content")
			if body.String("msg_type") != "post" || !strings.Contains(content, "🔒 未授权。请在 Claude Code 桌面端生成配对码后发送给我。") {
				t.Error("original pairing reply missing", content)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"private-notice"}}`))
			replyOnce.Do(func() { close(replied) })
		default:
			t.Errorf("unexpected API %s", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer peer.Close()
	address = peer.URL
	previousClient, previousRuntime := newNativeFeishuClient, newNativeFeishuRuntime
	newNativeFeishuClient = func(ctx context.Context, o imruntime.FeishuClientOptions) (*imruntime.FeishuClient, error) {
		u, _ := url.Parse(peer.URL)
		o.HTTP = &http.Client{Transport: feishuCLITransport{u, peer.Client().Transport}}
		o.Cache = imruntime.NewFeishuTokenCache()
		return imruntime.NewFeishuClient(ctx, o)
	}
	newNativeFeishuRuntime = func(ctx context.Context, o imruntime.FeishuRuntimeOptions) (*imruntime.FeishuRuntime, error) {
		o.WSOptions.Dial = func(ctx context.Context, address string, _ *tls.Config) (imruntime.FeishuSocket, error) {
			return imruntime.DialFeishuSocket(ctx, address, peer.Client().Transport.(*http.Transport).TLSClientConfig)
		}
		return imruntime.NewFeishuRuntime(ctx, o)
	}
	defer func() { newNativeFeishuClient, newNativeFeishuRuntime = previousClient, previousRuntime }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan int, 1)
	go func() {
		finished <- run(ctx, []string{"adapters", "--app-root", "/private/fixture/app.asar", "--feishu"})
	}()
	for _, ch := range []<-chan struct{}{acked, replied} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("ordinary factory missing real binary ACK/reply")
		}
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actual launcher Close/join")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("actual platform socket survived Close")
	}
	stored, err := os.ReadFile(filepath.Join(config, "adapters.json"))
	if err != nil || string(stored) != string(seed) {
		t.Fatal("original stored config changed", err)
	}
}

func mustFeishuCLIJSON(v any) []byte { b, _ := json.Marshal(v); return b }
