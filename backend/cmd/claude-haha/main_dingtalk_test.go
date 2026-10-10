package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
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

type dingTalkCLITransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (d dingTalkCLITransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.dingtalk.com" {
		return d.next.RoundTrip(req)
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = d.target.Scheme, d.target.Host
	copy.URL = &u
	return d.next.RoundTrip(copy)
}
func dingTalkCLIObject(value json.RawMessage) protocol.JSONObject {
	o := protocol.NewJSONObject()
	_ = o.UnmarshalJSON(value)
	return o
}

func TestPublicGoCLIAdapterDefaultDingTalkFactoryActualGatewayACKPairingReplyAndClose(t *testing.T) {
	actualPublicDingTalkFactory(t, false)
}
func TestPublicDingTalkTerminalStreamErrorExitsActualMainLauncher(t *testing.T) {
	actualPublicDingTalkFactory(t, true)
}
func actualPublicDingTalkFactory(t *testing.T, terminal bool) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(key, home)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "DINGTALK_CLIENT_ID", "DINGTALK_CLIENT_SECRET", "NODE_EXTRA_CA_CERTS"} {
		t.Setenv(key, "")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("ADAPTER_SERVER_URL", "ws://127.0.0.1:1")
	t.Setenv("ADAPTER_DEFAULT_PROJECT_DIR", home)
	t.Setenv("CALLER_DIR", home)
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	seed := []byte(`{"dingtalk":{"clientId":"private-id","clientSecret":"private-secret","endpoint":"wss://unused-source-config","allowedUsers":[]},"future":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), seed, 0600); err != nil {
		t.Fatal(err)
	}
	replied, acked, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var replyOnce, ackOnce, closeOnce sync.Once
	var address string
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer func() { conn.Close(); closeOnce.Do(func() { close(closed) }) }()
		if conn.Request().URL.RawQuery != "ticket=private-ticket" {
			t.Error("original ticket query lost")
		}
		data := map[string]any{"msgId": "private-body", "conversationType": "1", "senderStaffId": "stranger", "senderNick": "tester", "sessionWebhook": address + "/reply", "msgtype": "text", "text": map[string]string{"content": "hello"}}
		body, _ := json.Marshal(data)
		frame := map[string]any{"type": "CALLBACK", "headers": map[string]string{"topic": imruntime.DingTalkRobotTopic, "messageId": "private-header"}, "data": string(body)}
		var sendErr error
		if terminal {
			sendErr = websocket.Message.Send(conn, "{invalid-json")
		} else {
			sendErr = websocket.JSON.Send(conn, frame)
		}
		if sendErr != nil {
			t.Error(sendErr)
			return
		}
		for {
			var response protocol.JSONObject
			if websocket.JSON.Receive(conn, &response) != nil {
				return
			}
			headers := dingTalkCLIObject(response.Get("headers"))
			if headers.String("messageId") == "private-header" {
				ackData := dingTalkCLIObject([]byte(response.String("data")))
				if string(response.Get("code")) != "200" || string(dingTalkCLIObject(ackData.Get("response")).Get("success")) != "true" {
					t.Error("original callback ACK changed")
				}
				ackOnce.Do(func() { close(acked) })
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
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		switch req.URL.Path {
		case "/v1.0/gateway/connections/open":
			if body.String("clientId") != "private-id" || body.String("clientSecret") != "private-secret" {
				t.Error("raw file credentials lost")
			}
			b, _ := json.Marshal(map[string]string{"endpoint": "wss" + strings.TrimPrefix(address, "https") + "/stream", "ticket": "private-ticket"})
			_, _ = w.Write(b)
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"private-access","expireIn":7200}`))
		case "/reply":
			if req.Header.Get("x-acs-dingtalk-access-token") != "private-access" {
				t.Error("real token boundary not consumed")
			}
			if dingTalkCLIObject(body.Get("markdown")).String("text") != "🔒 未授权。请先在 Claude Code 桌面端完成钉钉扫码绑定，再生成 IM 配对码后发送给我。" {
				t.Error("original pairing reply changed")
			}
			_, _ = w.Write([]byte(`{}`))
			replyOnce.Do(func() { close(replied) })
		default:
			t.Errorf("unexpected actual API %s", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer peer.Close()
	address = peer.URL
	previousAPI, previousStream := newNativeDingTalkAPI, newNativeDingTalkStream
	newNativeDingTalkAPI = func(ctx context.Context, o imruntime.DingTalkAPIOptions) (*imruntime.DingTalkAPI, error) {
		u, _ := url.Parse(peer.URL)
		o.HTTP = &http.Client{Transport: dingTalkCLITransport{u, peer.Client().Transport}}
		return imruntime.NewDingTalkAPI(ctx, o)
	}
	newNativeDingTalkStream = func(ctx context.Context, o imruntime.DingTalkStreamOptions) (*imruntime.DingTalkStream, error) {
		o.Dial = func(ctx context.Context, address string, _ *tls.Config) (*imruntime.Socket, error) {
			if !strings.HasPrefix(address, "wss"+strings.TrimPrefix(peer.URL, "https")+"/stream?ticket=") {
				t.Error("source gateway endpoint not consumed")
			}
			return imruntime.DialSocket(ctx, address, peer.Client().Transport.(*http.Transport).TLSClientConfig)
		}
		return imruntime.NewDingTalkStream(ctx, o)
	}
	defer func() { newNativeDingTalkAPI, newNativeDingTalkStream = previousAPI, previousStream }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan int, 1)
	go func() {
		finished <- run(ctx, []string{"adapters", "--app-root", "/private/fixture/app.asar", "--dingtalk"})
	}()
	if !terminal {
		for _, ch := range []<-chan struct{}{acked, replied} {
			select {
			case <-ch:
			case <-time.After(5 * time.Second):
				t.Fatal("ordinary default DingTalk factory did not ACK and deliver original reply")
			}
		}
		cancel()
	}
	select {
	case code := <-finished:
		expected := 0
		if terminal {
			expected = 1
		}
		if code != expected {
			t.Fatal(code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary launcher did not expose terminal stream error or Close/join")
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
