package imruntime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func dingTalkStreamFrames() []protocol.JSONObject {
	f := []protocol.JSONObject{}
	for _, s := range []string{`{"type":"SYSTEM","headers":{"topic":"REGISTERED"}}`, `{"type":"SYSTEM","headers":{"topic":"ping","messageId":"sys","future":true},"data":{"keep":1}}`, `{"type":"EVENT","headers":{"topic":"future","messageId":"event"},"data":"event"}`, `{"type":"CALLBACK","headers":{"topic":"/v1.0/im/bot/messages/get","messageId":"h\ud800"},"data":"{\"msgId\":\"b\\ud800\",\"conversationType\":\"1\",\"senderId\":\"U\",\"text\":{\"content\":\"hello\\udfff\"}}"}`, `{"type":"CALLBACK","headers":{"topic":"/v1.0/im/bot/messages/get","messageId":"h\ud800"},"data":{"msgId":"other"}}`, `{"type":"CALLBACK","headers":{"topic":"/v1.0/im/bot/messages/get","messageId":"different"},"data":{"msgId":"b\ud800"}}`, `{"type":"CALLBACK","headers":{"topic":"/v1.0/card/instances/callback","messageId":"card"},"data":{"requestId":"r","action":"allow"}}`, `{"type":"CALLBACK","headers":{"topic":"/v1.0/card/instances/callback","messageId":"card"},"data":{"requestId":"other"}}`, `{"type":"CALLBACK","headers":{"topic":"ignored","messageId":"unused"},"data":{}}`, `{"type":"SYSTEM","headers":{"topic":"KEEPALIVE"}}`} {
		f = append(f, object([]byte(s)))
	}
	return f
}
func TestDingTalkActualSDKAndIndexACKDedupRawFrames(t *testing.T) {
	credentials := object([]byte(`{"clientId":1e400,"clientSecret":"s\ud800"}`))
	frames := dingTalkStreamFrames()
	input := map[string]any{"credentials": credentials, "frames": frames}
	want := runDingTalkSource(t, "dingtalk-stream.fixture.ts", input)
	again := runDingTalkSource(t, "dingtalk-stream.fixture.ts", input)
	exactJSON(t, raw(want), raw(again), "Source replay")
	var mu sync.Mutex
	wires := []protocol.JSONObject{}
	robots, cards := []json.RawMessage{}, []json.RawMessage{}
	peer := httptest.NewServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var wire string
			if websocket.Message.Receive(conn, &wire) != nil {
				return
			}
			mu.Lock()
			wires = append(wires, object([]byte(wire)))
			mu.Unlock()
		}
	}})
	defer peer.Close()
	socket, e := DialSocket(context.Background(), "ws"+strings.TrimPrefix(peer.URL, "http"), nil)
	if e != nil {
		t.Fatal(e)
	}
	api, e := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials})
	if e != nil {
		t.Fatal(e)
	}
	defer api.Close()
	client, e := NewDingTalkStream(context.Background(), DingTalkStreamOptions{API: api})
	if e != nil {
		t.Fatal(e)
	}
	client.socket = socket
	dedup := NewMessageDedup(context.Background())
	defer dedup.Close()
	dispatch := &DingTalkDispatcher{Stream: client, Dedup: dedup, Robot: func(_ context.Context, v json.RawMessage) error {
		mu.Lock()
		robots = append(robots, v)
		mu.Unlock()
		return nil
	}, Card: func(_ context.Context, v json.RawMessage) error {
		mu.Lock()
		cards = append(cards, v)
		mu.Unlock()
		return nil
	}}
	client.o.OnRobot = dispatch.HandleRobot
	client.o.OnCard = dispatch.HandleCard
	for _, frame := range frames {
		if e = client.handle(frame); e != nil {
			t.Fatal(e)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		done := len(wires) == 7 && len(robots) == 1 && len(cards) == 1
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual ACK/business did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	state := client.Snapshot()
	client.Close()
	mu.Lock()
	out := protocol.NewJSONObject(dingTalkField("wires", wires), dingTalkField("robots", robots), dingTalkField("cards", cards), dingTalkField("state", state))
	mu.Unlock()
	exactJSON(t, raw(out), raw(want), "actual socket writes and dispatcher")
}
func TestDingTalkActualInstalledSDKPhysicalGatewayTLSAndReconnect(t *testing.T) {
	run := func(source bool) (protocol.JSONObject, []protocol.JSONObject, []protocol.JSONObject) {
		var mu sync.Mutex
		wires, requests := []protocol.JSONObject{}, []protocol.JSONObject{}
		observedACKs := make(chan struct{}, 2)
		connections := 0
		peer := httptest.NewTLSServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
			defer conn.Close()
			mu.Lock()
			connections++
			n := connections
			mu.Unlock()
			_ = websocket.Message.Send(conn, `{"type":"SYSTEM","headers":{"topic":"REGISTERED"}}`)
			frame := protocol.NewJSONObject(dingTalkField("type", "CALLBACK"), dingTalkField("headers", protocol.NewJSONObject(dingTalkField("topic", DingTalkRobotTopic), dingTalkField("messageId", n))), dingTalkField("data", protocol.NewJSONObject(dingTalkField("msgId", n), dingTalkField("senderId", "U"), dingTalkField("conversationType", "1"))))
			_ = websocket.Message.Send(conn, string(raw(frame)))
			var wire string
			if websocket.Message.Receive(conn, &wire) == nil {
				mu.Lock()
				wires = append(wires, object([]byte(wire)))
				mu.Unlock()
				observedACKs <- struct{}{}
			}
			if n == 1 {
				return
			}
			for {
				if websocket.Message.Receive(conn, &wire) != nil {
					return
				}
			}
		}})
		defer peer.Close()
		address := "wss" + strings.TrimPrefix(peer.URL, "https")
		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			mu.Lock()
			requests = append(requests, protocol.NewJSONObject(dingTalkField("path", r.URL.Path), dingTalkField("method", r.Method), dingTalkField("body", NewText(string(data))), dingTalkField("accept", r.Header.Get("Accept")), dingTalkField("ua", r.Header.Get("User-Agent")), dingTalkField("encoding", r.Header.Get("Accept-Encoding"))))
			mu.Unlock()
			_, _ = w.Write(raw(map[string]any{"endpoint": address, "ticket": "fixed"}))
		}))
		defer gateway.Close()
		credentials := object([]byte(`{"clientId":9007199254740993,"clientSecret":true}`))
		var result protocol.JSONObject
		if source {
			result = runDingTalkSource(t, "dingtalk-stream.fixture.ts", map[string]any{"credentials": credentials, "base": gateway.URL, "ca": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw})), "expectRobots": 2})
		} else {
			base, _ := url.Parse(gateway.URL)
			api, e := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials, HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}})
			if e != nil {
				t.Fatal(e)
			}
			defer api.Close()
			roots := x509.NewCertPool()
			roots.AddCert(peer.Certificate())
			robots := []json.RawMessage{}
			arrived := make(chan struct{}, 2)
			dispatch := &DingTalkDispatcher{Dedup: NewMessageDedup(context.Background())}
			defer dispatch.Dedup.Close()
			client, e := NewDingTalkStream(context.Background(), DingTalkStreamOptions{API: api, TLS: &tls.Config{RootCAs: roots}, ReconnectInterval: 20 * time.Millisecond, HeartbeatInterval: 100 * time.Millisecond, OnRobot: dispatch.HandleRobot})
			if e != nil {
				t.Fatal(e)
			}
			dispatch.Stream = client
			dispatch.Robot = func(_ context.Context, data json.RawMessage) error {
				mu.Lock()
				robots = append(robots, data)
				mu.Unlock()
				arrived <- struct{}{}
				return nil
			}
			if e = client.Connect(context.Background()); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 2; i++ {
				select {
				case <-arrived:
				case <-time.After(5 * time.Second):
					client.Close()
					t.Fatal("private actual reconnect callback not observed")
				}
			}
			for i := 0; i < 2; i++ {
				select {
				case <-observedACKs:
				case <-time.After(time.Second):
					client.Close()
					t.Fatal("actual private peer did not observe both callback ACKs")
				}
			}
			client.Close()
			mu.Lock()
			result = protocol.NewJSONObject(dingTalkField("initial", map[string]any{"heartbeat": 8000, "reconnect": 1000}), dingTalkField("robots", robots), dingTalkField("cards", []any{}), dingTalkField("connections", connections))
			mu.Unlock()
		}
		if source {
			for i := 0; i < 2; i++ {
				select {
				case <-observedACKs:
				case <-time.After(time.Second):
					t.Fatal("actual Source peer did not observe both callback ACKs")
				}
			}
		}
		mu.Lock()
		defer mu.Unlock()
		return result, append([]protocol.JSONObject{}, wires...), append([]protocol.JSONObject{}, requests...)
	}
	want, wantWires, wantReq := run(true)
	again, wires, req := run(true)
	exactJSON(t, raw(again), raw(want), "Source replay result")
	exactJSON(t, raw(wires), raw(wantWires), "Source replay WSS")
	for _, r := range wantReq {
		body := object([]byte(TextJSON(r.Get("body")).String()))
		if body.String("ua") != "" {
			t.Fatal("Source startup ua changed")
		}
	}
	got, gotWires, gotReq := run(false)
	exactJSON(t, raw(got), raw(want), "actual SDK reconnect result")
	exactJSON(t, raw(gotWires), raw(wantWires), "actual SDK reconnect ACKs")
	exactJSON(t, raw(req), raw(wantReq), "Source gateway replay")
	exactJSON(t, raw(gotReq), raw(wantReq), "actual fixed official gateway requests")
}
