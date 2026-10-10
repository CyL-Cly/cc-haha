package imruntime

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func dingTalkServerFrame(conn net.Conn, op byte, payload []byte) error {
	head := []byte{0x80 | op}
	if len(payload) < 126 {
		head = append(head, byte(len(payload)))
	} else {
		head = append(head, 126, byte(len(payload)>>8), byte(len(payload)))
	}
	_, e := conn.Write(append(head, payload...))
	return e
}
func dingTalkReadClientFrame(reader *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, e := io.ReadFull(reader, h[:]); e != nil {
		return 0, nil, e
	}
	if h[1]&0x80 == 0 {
		return 0, nil, fmt.Errorf("actual SDK must mask client frames")
	}
	size := int(h[1] & 127)
	if size == 126 {
		var b [2]byte
		if _, e := io.ReadFull(reader, b[:]); e != nil {
			return 0, nil, e
		}
		size = int(binary.BigEndian.Uint16(b[:]))
	}
	if size == 127 {
		return 0, nil, fmt.Errorf("unexpected large private frame")
	}
	var mask [4]byte
	if _, e := io.ReadFull(reader, mask[:]); e != nil {
		return 0, nil, e
	}
	body := make([]byte, size)
	if _, e := io.ReadFull(reader, body); e != nil {
		return 0, nil, e
	}
	for i := range body {
		body[i] ^= mask[i%4]
	}
	return h[0] & 15, body, nil
}
func dingTalkUpgrade(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	conn, buf, e := w.(http.Hijacker).Hijack()
	if e != nil {
		return nil, nil, e
	}
	sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	_, e = fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
	if e == nil {
		e = buf.Flush()
	}
	return conn, buf, e
}
func TestDingTalkInstalledSDKMissingNativePongTerminatesAndReconnects(t *testing.T) {
	run := func(source bool) (protocol.JSONObject, []protocol.JSONObject) {
		var mu sync.Mutex
		count := 0
		frames := []protocol.JSONObject{}
		completed := make(chan struct{})
		fail := make(chan error, 2)
		peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, buf, e := dingTalkUpgrade(w, r)
			if e != nil {
				fail <- e
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			mu.Lock()
			count++
			n := count
			mu.Unlock()
			_ = dingTalkServerFrame(conn, 1, []byte(`{"type":"SYSTEM","headers":{"topic":"REGISTERED"}}`))
			sent := false
			for {
				op, data, e := dingTalkReadClientFrame(buf.Reader)
				if e != nil {
					if n == 1 {
						return
					}
					fail <- e
					return
				}
				mu.Lock()
				frames = append(frames, protocol.NewJSONObject(dingTalkField("connection", n), dingTalkField("op", op), dingTalkField("body", NewText(string(data)))))
				mu.Unlock()
				if n == 1 {
					continue
				}
				if op == 9 {
					_ = dingTalkServerFrame(conn, 10, data)
					if !sent {
						sent = true
						_ = dingTalkServerFrame(conn, 1, []byte(`{"type":"CALLBACK","headers":{"topic":"/v1.0/im/bot/messages/get","messageId":"after-pong"},"data":{"msgId":"b","senderId":"U"}}`))
					}
				}
				if op == 1 {
					close(completed)
					return
				}
			}
		}))
		defer peer.Close()
		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(raw(map[string]any{"endpoint": "wss" + strings.TrimPrefix(peer.URL, "https"), "ticket": "fixed"}))
		}))
		defer gateway.Close()
		credentials := object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"}))
		var out protocol.JSONObject
		if source {
			out = runDingTalkSource(t, "dingtalk-stream.fixture.ts", map[string]any{"base": gateway.URL, "credentials": credentials, "ca": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}))})
		} else {
			base, _ := url.Parse(gateway.URL)
			api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials, HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}})
			defer api.Close()
			roots := x509.NewCertPool()
			roots.AddCert(peer.Certificate())
			dispatch := &DingTalkDispatcher{Dedup: NewMessageDedup(context.Background())}
			defer dispatch.Dedup.Close()
			received := make(chan json.RawMessage, 1)
			dispatch.Robot = func(_ context.Context, v json.RawMessage) error { received <- v; return nil }
			client, e := NewDingTalkStream(context.Background(), DingTalkStreamOptions{API: api, TLS: &tls.Config{RootCAs: roots}, HeartbeatInterval: 100 * time.Millisecond, ReconnectInterval: 20 * time.Millisecond, OnRobot: dispatch.HandleRobot})
			if e != nil {
				t.Fatal(e)
			}
			dispatch.Stream = client
			if e = client.Connect(context.Background()); e != nil {
				t.Fatal(e)
			}
			var payload json.RawMessage
			select {
			case payload = <-received:
			case <-time.After(5 * time.Second):
				client.Close()
				t.Fatal("missing pong didn't reconnect")
			}
			client.Close()
			mu.Lock()
			out = protocol.NewJSONObject(dingTalkField("initial", map[string]any{"heartbeat": 8000, "reconnect": 1000}), dingTalkField("robots", []json.RawMessage{payload}), dingTalkField("cards", []any{}), dingTalkField("connections", count))
			mu.Unlock()
		}
		select {
		case <-completed:
		case e := <-fail:
			t.Fatal(e)
		case <-time.After(5 * time.Second):
			t.Fatal("reconnect ACK didn't reach physical peer")
		}
		mu.Lock()
		defer mu.Unlock()
		if count != 2 {
			t.Fatalf("actual reconnect count %d want 2", count)
		}
		return out, append([]protocol.JSONObject{}, frames...)
	}
	want, wantWire := run(true)
	again, againWire := run(true)
	exactJSON(t, raw(again), raw(want), "Source missingpong result replay")
	exactJSON(t, raw(againWire), raw(wantWire), "Source missingpong frame replay")
	got, gotWire := run(false)
	exactJSON(t, raw(got), raw(want), "native missingpong/reconnect state")
	exactJSON(t, raw(gotWire), raw(wantWire), "native missingpong/masked ping exact frames")
}
func TestDingTalkInstalledSDKNativeMaskedPingPongAndCompressedCallback(t *testing.T) {
	run := func(source bool) (protocol.JSONObject, []protocol.JSONObject) {
		var mu sync.Mutex
		wire := []protocol.JSONObject{}
		done := make(chan struct{})
		errors := make(chan error, 1)
		peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(done)
			conn, buf, e := w.(http.Hijacker).Hijack()
			if e != nil {
				errors <- e
				return
			}
			defer conn.Close()
			if r.Header.Get("Sec-WebSocket-Extensions") != "permessage-deflate; client_max_window_bits" {
				errors <- fmt.Errorf("original PMD offer missing: %s", r.Header.Get("Sec-WebSocket-Extensions"))
				return
			}
			sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			_, e = fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
			if e != nil {
				errors <- e
				return
			}
			_ = buf.Flush()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_ = dingTalkServerFrame(conn, 1, []byte(`{"type":"SYSTEM","headers":{"topic":"REGISTERED"}}`))
			_ = dingTalkServerFrame(conn, 9, []byte("physical-ping"))
			sent := false
			for {
				op, data, e := dingTalkReadClientFrame(buf.Reader)
				if e != nil {
					errors <- e
					return
				}
				mu.Lock()
				wire = append(wire, protocol.NewJSONObject(dingTalkField("op", op), dingTalkField("payload", NewText(string(data)))))
				mu.Unlock()
				if op == 9 {
					_ = dingTalkServerFrame(conn, 10, data)
					if !sent {
						sent = true
						frame := protocol.NewJSONObject(dingTalkField("type", "CALLBACK"), dingTalkField("headers", protocol.NewJSONObject(dingTalkField("topic", DingTalkRobotTopic), dingTalkField("messageId", "compressed"))), dingTalkField("data", protocol.NewJSONObject(dingTalkField("msgId", "body"), dingTalkField("text", protocol.NewJSONObject(dingTalkField("content", strings.Repeat("native😀", 100)))))))
						var buffer bytes.Buffer
						zip, _ := flate.NewWriter(&buffer, flate.DefaultCompression)
						_, _ = zip.Write(raw(frame))
						_ = zip.Flush()
						encoded := append([]byte{}, buffer.Bytes()...)
						_ = zip.Close()
						if len(encoded) < 4 {
							errors <- fmt.Errorf("bad private deflate")
							return
						}
						_ = dingTalkServerFrame(conn, 0x41, encoded[:len(encoded)-4])
					}
				}
				if op == 1 {
					if object(data).String("message") != "OK" {
						errors <- fmt.Errorf("callback ACK missing")
						return
					}
					return
				}
			}
		}))
		defer peer.Close()
		gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(raw(map[string]any{"endpoint": "wss" + strings.TrimPrefix(peer.URL, "https"), "ticket": "fixed"}))
		}))
		defer gateway.Close()
		credentials := object(raw(map[string]any{"clientId": "id", "clientSecret": "secret"}))
		var result protocol.JSONObject
		if source {
			result = runDingTalkSource(t, "dingtalk-stream.fixture.ts", map[string]any{"base": gateway.URL, "credentials": credentials, "ca": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}))})
		} else {
			base, _ := url.Parse(gateway.URL)
			api, _ := NewDingTalkAPI(context.Background(), DingTalkAPIOptions{Credentials: credentials, HTTP: &http.Client{Transport: dingTalkRedirect{base, http.DefaultTransport}}})
			defer api.Close()
			roots := x509.NewCertPool()
			roots.AddCert(peer.Certificate())
			dispatch := &DingTalkDispatcher{Dedup: NewMessageDedup(context.Background())}
			defer dispatch.Dedup.Close()
			robots := []protocol.JSONObject{}
			arrived := make(chan struct{})
			client, e := NewDingTalkStream(context.Background(), DingTalkStreamOptions{API: api, TLS: &tls.Config{RootCAs: roots}, HeartbeatInterval: 100 * time.Millisecond, OnRobot: dispatch.HandleRobot})
			if e != nil {
				t.Fatal(e)
			}
			dispatch.Stream = client
			dispatch.Robot = func(_ context.Context, v json.RawMessage) error {
				robots = append(robots, object(v))
				close(arrived)
				return nil
			}
			if e = client.Connect(context.Background()); e != nil {
				t.Fatal(e)
			}
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				client.Close()
				t.Fatal("compressed actual callback not delivered")
			}
			client.Close()
			result = protocol.NewJSONObject(dingTalkField("initial", map[string]any{"heartbeat": 8000, "reconnect": 1000}), dingTalkField("robots", robots), dingTalkField("cards", []any{}), dingTalkField("connections", 1))
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("physical socket didn't join")
		}
		select {
		case e := <-errors:
			t.Fatal(e)
		default:
		}
		mu.Lock()
		defer mu.Unlock()
		return result, append([]protocol.JSONObject{}, wire...)
	}
	want, wantWire := run(true)
	again, againWire := run(true)
	exactJSON(t, raw(again), raw(want), "Source native callback replay")
	exactJSON(t, raw(againWire), raw(wantWire), "Source controls replay")
	got, gotWire := run(false)
	exactJSON(t, raw(got), raw(want), "native deflate payload")
	exactJSON(t, raw(gotWire), raw(wantWire), "actual masked ping/pong/ACK")
}
