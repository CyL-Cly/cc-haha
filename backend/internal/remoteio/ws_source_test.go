package remoteio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

type sourceWSPeer struct {
	server    *httptest.Server
	mu        sync.Mutex
	messages  []protocol.JSONObject
	headers   []string
	connected chan struct{}
	conn      *websocket.Conn
}

func newSourceWSPeer(t *testing.T, ctx context.Context) *sourceWSPeer {
	p := &sourceWSPeer{connected: make(chan struct{})}
	consume := func(message protocol.JSONObject) {
		p.mu.Lock()
		p.messages = append(p.messages, message.Clone())
		conn := p.conn
		p.mu.Unlock()
		if message.String("type") == "result" {
			if err := conn.Write(ctx, websocket.MessageText, []byte("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"real remote input\"}}\n")); err != nil {
				t.Error(err)
			}
			_ = conn.Close(websocket.StatusCode(4001), "private permanently retired")
		}
	}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.headers = append(p.headers, r.Method+"|"+r.URL.RequestURI()+"|"+r.Header.Get("Authorization"))
		p.mu.Unlock()
		if r.Method == "POST" {
			var body struct {
				Events []protocol.JSONObject `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			for _, event := range body.Events {
				consume(event)
			}
			_, _ = io.WriteString(w, `{}`)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		p.mu.Lock()
		p.conn = conn
		p.mu.Unlock()
		close(p.connected)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var frame protocol.JSONObject
			if err = frame.UnmarshalJSON(data); err != nil {
				t.Error(err)
				return
			}
			consume(frame)
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}
func (p *sourceWSPeer) snapshot() ([]protocol.JSONObject, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]protocol.JSONObject(nil), p.messages...), append([]string(nil), p.headers...)
}

func TestActualSourceNativeWebSocketAndHybridHTTPOrderedPayloadsAndClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, hybrid := range []bool{false, true} {
		t.Run(map[bool]string{false: "websocket", true: "hybrid"}[hybrid], func(t *testing.T) {
			env := map[string]string{"CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-native-transport"}
			if hybrid {
				env["CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2"] = "1"
			}
			messages := []protocol.JSONObject{object([]byte(`{"type":"stream_event","uuid":"source-delta","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"actual transport data"}}}`)), object([]byte(`{"type":"control_request","request_id":"source-request","request":{"subtype":"can_use_tool","tool_name":"Read","input":{"file_path":"/private/fixture"}}}`)), object([]byte(`{"type":"result","uuid":"source-result","result":"real output"}`))}
			old := newSourceWSPeer(t, ctx)
			url := strings.Replace(old.server.URL, "http", "ws", 1) + "/ws/source?opaque=1"
			want := runSourceFixture(t, "native-remoteio-ws.fixture.ts", map[string]any{"url": url, "env": env, "messages": messages})
			peer := newSourceWSPeer(t, ctx)
			url = strings.Replace(peer.server.URL, "http", "ws", 1) + "/ws/source?opaque=1"
			clientConnected := make(chan struct{})
			service, err := New(ctx, Options{URL: url, Environment: env, OnConnect: func() { close(clientConnected) }})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			service.Start()
			select {
			case <-clientConnected:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			for _, message := range messages {
				if err = service.WriteMessage(message); err != nil {
					t.Fatal(err)
				}
			}
			if err = service.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(service)
			if err != nil {
				t.Fatal(err)
			}
			if err = service.Close(); err != nil {
				t.Fatal(err)
			}
			oldMessages, oldHeaders := old.snapshot()
			goMessages, goHeaders := peer.snapshot()
			var a, b any
			_ = json.Unmarshal(raw(oldMessages), &a)
			_ = json.Unmarshal(raw(goMessages), &b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("Source actual payload%s Go%s", raw(oldMessages), raw(goMessages))
			}
			if !reflect.DeepEqual(oldHeaders, goHeaders) {
				t.Errorf("Source actual HTTP/WS headers%v Go%v", oldHeaders, goHeaders)
			}
			if string(data) != want.String("incoming") || service.CloseCode() != 4001 || string(want.Get("code")) != "4001" {
				t.Fatal("actual close/input differs", want.String("incoming"), string(data), service.CloseCode())
			}
		})
	}
}
