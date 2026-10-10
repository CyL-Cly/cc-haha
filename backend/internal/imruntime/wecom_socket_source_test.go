package imruntime

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

type wecomSocketScript struct {
	mode        string
	mu          sync.Mutex
	frames      []protocol.JSONObject
	connections int
	first       chan struct{}
}

func wecomTestCallback() protocol.JSONObject {
	return object([]byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"inbound"},"body":{"msgtype":"text","from":{"userid":"U"},"text":{"content":"hello\ud800"},"future":{"keep":true}}}`))
}
func wecomTestEvent(kind string) protocol.JSONObject {
	return wecomJSONObject(wecomField("cmd", "aibot_event_callback"), wecomField("headers", wecomJSONObject(wecomField("req_id", "event"))), wecomField("body", wecomJSONObject(wecomField("msgtype", "event"), wecomField("event", wecomJSONObject(wecomField("eventtype", kind))))))
}
func (p *wecomSocketScript) serve(conn *websocket.Conn) {
	defer conn.Close()
	p.mu.Lock()
	p.connections++
	attempt := p.connections
	p.mu.Unlock()
	var held protocol.JSONObject
	first := true
	for {
		var wire string
		if websocket.Message.Receive(conn, &wire) != nil {
			return
		}
		frame := object([]byte(wire))
		cmd := frame.String("cmd")
		copy := object(raw(frame))
		id := wecomReqID(frame)
		if strings.HasPrefix(id.String(), cmd+"_") {
			copy.Set("headers", raw(wecomJSONObject(wecomField("req_id", cmd))))
		}
		p.mu.Lock()
		p.frames = append(p.frames, copy)
		p.mu.Unlock()
		if cmd == "aibot_subscribe" {
			if p.mode == "auth" {
				_ = websocket.Message.Send(conn, string(raw(wecomJSONObject(wecomField("headers", object(frame.Get("headers"))), wecomField("errcode", 42), wecomField("errmsg", "denied")))))
				continue
			}
			if p.mode == "reconnect" {
				return
			}
			_ = wecomAck(conn, frame, nil)
			if p.mode == "heartbeat" {
				if attempt > 1 {
					_ = websocket.Message.Send(conn, string(raw(wecomTestEvent("disconnected_event"))))
				}
				continue
			}
			_ = websocket.Message.Send(conn, string(raw(wecomTestCallback())))
			_ = websocket.Message.Send(conn, string(raw(wecomTestEvent("enter_chat"))))
			_ = websocket.Message.Send(conn, `{"headers":{"req_id":"unknown"},"body":{"shouldNotBeMessage":true}}`)
			continue
		}
		if cmd == "ping" {
			continue
		}
		if id.String() == "r1" && first {
			first = false
			held = frame
			if p.first != nil {
				close(p.first)
			}
			continue
		}
		if id.String() == "r2" {
			if p.mode == "disconnect" {
				_ = websocket.Message.Send(conn, string(raw(wecomTestEvent("disconnected_event"))))
				continue
			}
			_ = wecomAck(conn, frame, map[string]string{"ack": "independent"})
			if p.mode == "timeout" {
				continue
			}
			if p.mode == "failure" {
				_ = websocket.Message.Send(conn, string(raw(wecomJSONObject(wecomField("headers", object(held.Get("headers"))), wecomField("errcode", 99), wecomField("errmsg", "bad"), wecomField("future", "kept")))))
			} else {
				_ = wecomAck(conn, held, map[string]string{"ack": "first"})
			}
			continue
		}
		_ = wecomAck(conn, frame, map[string]string{"ack": "second"})
	}
}
func (p *wecomSocketScript) snapshot() []protocol.JSONObject {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]protocol.JSONObject{}, p.frames...)
}
func wecomCaptureReply(f protocol.JSONObject, e error) protocol.JSONObject {
	if e != nil {
		var ack *WecomAckError
		if errors.As(e, &ack) {
			return wecomJSONObject(wecomField("error", ack.Frame))
		}
		return wecomJSONObject(wecomField("error", e.Error()))
	}
	return wecomJSONObject(wecomField("value", f))
}

func TestWecomOriginalSDKActualTLSAuthQueueErrorTimeoutHeartbeatAndReconnect(t *testing.T) {
	for _, mode := range []string{"normal", "failure", "timeout", "disconnect", "auth", "heartbeat", "reconnect"} {
		t.Run(mode, func(t *testing.T) {
			sourceScript := &wecomSocketScript{mode: mode, frames: []protocol.JSONObject{}}
			sourcePeer, _ := wecomPrivatePeer(t, sourceScript.serve)
			ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: sourcePeer.Certificate().Raw}))
			want := runWecomFixture(t, "wecom-socket.fixture.ts", map[string]any{"mode": mode, "url": "wss" + strings.TrimPrefix(sourcePeer.URL, "https"), "ca": ca, "botId": 17, "secret": true})
			sourcePeer.Close()
			goScript := &wecomSocketScript{mode: mode, frames: []protocol.JSONObject{}, first: make(chan struct{})}
			peer, dial := wecomPrivatePeer(t, goScript.serve)
			defer peer.Close()
			var eventsMu sync.Mutex
			events := []protocol.JSONObject{}
			appendEvent := func(e protocol.JSONObject) { eventsMu.Lock(); events = append(events, e); eventsMu.Unlock() }
			ready := make(chan struct{}, 4)
			terminal := make(chan struct{}, 1)
			n := 2
			heartbeat := 30 * time.Second
			if mode == "heartbeat" {
				heartbeat = 20 * time.Millisecond
			}
			client, e := NewWecomClient(context.Background(), WecomClientOptions{Credentials: wecomJSONObject(wecomField("bot_id", 17), wecomField("secret", true)), Dial: dial, HeartbeatInterval: heartbeat, ReconnectBaseDelay: time.Millisecond, MaxAuthFailureAttempts: &n, MaxReconnectAttempts: &n,
				OnAuthenticated: func() { appendEvent(wecomJSONObject(wecomField("authenticated", true))); ready <- struct{}{} },
				OnMessage:       func(f protocol.JSONObject) { appendEvent(wecomJSONObject(wecomField("message", f))) },
				OnEvent: func(f protocol.JSONObject) {
					appendEvent(wecomJSONObject(wecomField("event", f)))
					if mode == "heartbeat" && object(object(f.Get("body")).Get("event")).String("eventtype") == "disconnected_event" {
						terminal <- struct{}{}
					}
				},
				OnDisconnected: func(s string) { appendEvent(wecomJSONObject(wecomField("disconnected", s))) },
				OnReconnecting: func(n int) { appendEvent(wecomJSONObject(wecomField("reconnecting", n))) },
				OnError: func(e error) {
					f := wecomJSONObject(wecomField("error", e.Error()))
					var exhausted *WecomLimitError
					if errors.As(e, &exhausted) {
						f.Set("code", raw(exhausted.Code))
						terminal <- struct{}{}
					}
					appendEvent(f)
				},
			})
			if e != nil {
				t.Fatal(e)
			}
			client.Start()
			defer client.Close()
			results := []protocol.JSONObject{}
			if mode == "auth" || mode == "heartbeat" || mode == "reconnect" {
				select {
				case <-terminal:
				case <-time.After(6 * time.Second):
					t.Fatal("terminal event not received")
				}
			} else {
				select {
				case <-ready:
				case <-time.After(5 * time.Second):
					t.Fatal("not authenticated")
				}
				firstDone, secondDone, otherDone := make(chan protocol.JSONObject, 1), make(chan protocol.JSONObject, 1), make(chan protocol.JSONObject, 1)
				frame := wecomJSONObject(wecomField("headers", wecomJSONObject(wecomField("req_id", "r1"))))
				go func() {
					f, e := client.ReplyStream(context.Background(), frame, NewText("bubble"), NewText("one"), false)
					firstDone <- wecomCaptureReply(f, e)
				}()
				select {
				case <-goScript.first:
				case <-time.After(5 * time.Second):
					t.Fatal("first physical frame missing")
				}
				go func() {
					f, e := client.ReplyStream(context.Background(), frame, NewText("bubble"), NewText("two"), true)
					secondDone <- wecomCaptureReply(f, e)
				}()
				// Observe admission of the queued item, not a guessed sleep/drain.
				deadline := time.NewTimer(5 * time.Second)
				tick := time.NewTicker(time.Millisecond)
				for {
					client.mu.Lock()
					queued := len(client.queues[wecomKey(NewText("r1"))])
					client.mu.Unlock()
					if queued == 2 {
						break
					}
					select {
					case <-tick.C:
					case <-deadline.C:
						t.Fatal("second reply was not queued")
					}
				}
				tick.Stop()
				deadline.Stop()
				go func() {
					f, e := client.ReplyStream(context.Background(), wecomJSONObject(wecomField("headers", wecomJSONObject(wecomField("req_id", "r2")))), NewText("bubble-2"), NewText("parallel"), false)
					otherDone <- wecomCaptureReply(f, e)
				}()
				for _, done := range []chan protocol.JSONObject{firstDone, secondDone, otherDone} {
					select {
					case result := <-done:
						results = append(results, result)
					case <-time.After(7 * time.Second):
						t.Fatal("reply did not settle")
					}
				}
			}
			client.Close()
			eventsMu.Lock()
			actualEvents := append([]protocol.JSONObject{}, events...)
			eventsMu.Unlock()
			exactJSON(t, raw(wecomJSONObject(wecomField("results", results), wecomField("events", actualEvents))), raw(want), mode)
			exactJSON(t, raw(goScript.snapshot()), raw(sourceScript.snapshot()), mode+" physical frames")
		})
	}
}

func TestWecomResponsePhysicalPendingIntermediateDoesNotBlockFinal(t *testing.T) {
	arrived := make(chan protocol.JSONObject, 8)
	permit := make(chan struct{}, 8)
	peer, dial := wecomPrivatePeer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var data string
			if websocket.Message.Receive(conn, &data) != nil {
				return
			}
			f := object([]byte(data))
			if f.String("cmd") == "aibot_subscribe" {
				_ = wecomAck(conn, f, nil)
				continue
			}
			arrived <- f
			<-permit
			_ = wecomAck(conn, f, nil)
		}
	})
	defer peer.Close()
	ready := make(chan struct{})
	client, e := NewWecomClient(context.Background(), WecomClientOptions{Dial: dial, OnAuthenticated: func() { close(ready) }})
	if e != nil {
		t.Fatal(e)
	}
	client.Start()
	defer client.Close()
	<-ready
	frame := wecomJSONObject(wecomField("headers", wecomJSONObject(wecomField("req_id", "turn-one"))))
	response := NewWecomResponse(context.Background(), client, NewText("U"), &frame, "fixed", nil)
	firstDone := make(chan error, 1)
	go func() { firstDone <- response.Append("one") }()
	<-arrived
	secondDone := make(chan error, 1)
	go func() { secondDone <- response.Append("two") }()
	select {
	case e := <-secondDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("intermediate waits for prior ACK instead of skipping")
	}
	finalDone := make(chan error, 1)
	go func() { finalDone <- response.Finish() }()
	permit <- struct{}{}
	if e := <-firstDone; e != nil {
		t.Fatal(e)
	}
	select {
	case f := <-arrived:
		stream := object(object(f.Get("body")).Get("stream"))
		if stream.String("content") != "onetwo" || string(stream.Get("finish")) != "true" {
			t.Fatalf("final lost skipped text: %s", raw(f))
		}
	case <-time.After(time.Second):
		t.Fatal("final frame was skipped")
	}
	permit <- struct{}{}
	if e := <-finalDone; e != nil {
		t.Fatal(e)
	}
}

var _ = json.Valid
var _ = fmt.Errorf
