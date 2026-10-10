package imruntime

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type qqTestTimers struct {
	mu     sync.Mutex
	now    int64
	next   int
	timers map[int]struct {
		d time.Duration
		f func()
	}
}

func (t *qqTestTimers) schedule(d time.Duration, f func()) func() {
	t.mu.Lock()
	t.next++
	id := t.next
	t.timers[id] = struct {
		d time.Duration
		f func()
	}{d, f}
	t.mu.Unlock()
	return func() { t.mu.Lock(); delete(t.timers, id); t.mu.Unlock() }
}
func (t *qqTestTimers) fire(test *testing.T, d time.Duration) {
	test.Helper()
	qqLeafUntil(test, func() bool {
		t.mu.Lock()
		defer t.mu.Unlock()
		for _, v := range t.timers {
			if v.d == d {
				return true
			}
		}
		return false
	})
	t.mu.Lock()
	at := 0
	var f func()
	for i, v := range t.timers {
		if v.d == d {
			at = i
			f = v.f
			break
		}
	}
	delete(t.timers, at)
	t.mu.Unlock()
	f()
}
func qqLeafUntil(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("actual QQ lifecycle barrier timeout")
		}
		time.Sleep(time.Millisecond)
	}
}
func qqServerFrame(conn net.Conn, data []byte, op byte) error {
	head := []byte{0x80 | op}
	if len(data) < 126 {
		head = append(head, byte(len(data)))
	} else {
		head = append(head, 126, byte(len(data)>>8), byte(len(data)))
	}
	_, e := conn.Write(append(head, data...))
	return e
}
func qqServerRead(reader *bufio.Reader) ([]byte, error) {
	var head [2]byte
	if _, e := io.ReadFull(reader, head[:]); e != nil {
		return nil, e
	}
	n := int(head[1] & 127)
	if n == 126 {
		var size [2]byte
		if _, e := io.ReadFull(reader, size[:]); e != nil {
			return nil, e
		}
		n = int(binary.BigEndian.Uint16(size[:]))
	}
	var mask [4]byte
	if _, e := io.ReadFull(reader, mask[:]); e != nil {
		return nil, e
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(reader, b); e != nil {
		return nil, e
	}
	for i := range b {
		b[i] ^= mask[i%4]
	}
	if head[0]&15 == 10 {
		return qqServerRead(reader)
	}
	if head[0]&15 == 8 {
		return nil, io.EOF
	}
	return b, nil
}
func TestQQActualVendorGatewayIdentifyResumeHeartbeatRefreshAndPhysicalClose(t *testing.T) {
	qqGatewayLifecycle(t, false)
}
func TestQQActualVendorCompressedFragmentedGatewayAndPhysicalClose(t *testing.T) {
	qqGatewayLifecycle(t, true)
}
func qqGatewayLifecycle(t *testing.T, compressed bool) {
	var want json.RawMessage
	var mu sync.Mutex
	frames := []protocol.JSONObject{}
	events := []protocol.JSONObject{}
	calls := []protocol.JSONObject{}
	connections, closed, tokens := 0, 0, 0
	hello := map[int]chan struct{}{}
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != QQUserAgent {
			t.Error("gateway UA")
		}
		conn, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		defer conn.Close()
		key := r.Header.Get("Sec-WebSocket-Key")
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		extension := ""
		if compressed {
			extension = "Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; client_no_context_takeover\r\n"
		}
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+base64.StdEncoding.EncodeToString(sum[:])+"\r\n"+extension+"\r\n")
		sendFrame := func(conn net.Conn, data []byte, op byte) error {
			if !compressed || op != 1 {
				return qqServerFrame(conn, data, op)
			}
			var encoded bytes.Buffer
			zip, _ := flate.NewWriter(&encoded, flate.BestSpeed)
			zip.Write(data)
			zip.Flush()
			body := append([]byte{}, encoded.Bytes()...)
			zip.Close()
			body = body[:len(body)-4]
			at := len(body) / 2
			if e := qqServerFragment(conn, body[:at], 0x41, false); e != nil {
				return e
			}
			if e := qqServerFrame(conn, []byte("private-ping"), 9); e != nil {
				return e
			}
			return qqServerFragment(conn, body[at:], 0, true)
		}

		mu.Lock()
		connections++
		at := connections
		hello[at] = make(chan struct{})
		helloReady := hello[at]
		mu.Unlock()
		<-helloReady
		defer func() { mu.Lock(); closed++; mu.Unlock() }()
		sendFrame(conn, raw(qqObject(qqField("op", 10), qqField("d", qqObject(qqField("heartbeat_interval", 1234))))), 1)
		for {
			packet, e := qqServerRead(rw.Reader)
			if e != nil {
				return
			}
			f := object(packet)
			mu.Lock()
			frames = append(frames, qqObject(qqField("connection", at), qqField("frame", f)))
			mu.Unlock()
			op := qqInt(f.Get("op"), -1)
			if op == 2 || op == 6 {
				if at == 1 {
					sendFrame(conn, raw(qqObject(qqField("op", 0), qqField("s", 5), qqField("t", "READY"), qqField("d", qqObject(qqField("session_id", "private-session-one"))))), 1)
					sendFrame(conn, raw(qqObject(qqField("op", 0), qqField("s", 6), qqField("t", "C2C_MESSAGE_CREATE"), qqField("d", qqObject(qqField("author", qqObject(qqField("user_openid", "private-user"))), qqField("content", "private-inbound"), qqField("id", "private-message"), qqField("timestamp", "private-stamp"))))), 1)
				} else if at == 2 {
					sendFrame(conn, raw(qqObject(qqField("op", 0), qqField("s", 7), qqField("t", "RESUMED"), qqField("d", qqObject(qqField("private", "resumed"))))), 1)
				} else {
					sendFrame(conn, raw(qqObject(qqField("op", 0), qqField("s", 8), qqField("t", "READY"), qqField("d", qqObject(qqField("session_id", "private-session-two"))))), 1)
				}
			}
			if op == 1 && at == 1 {
				b := []byte{byte(4004 >> 8), byte(4004 & 255)}
				sendFrame(conn, append(b, []byte("private-refresh")...), 8)
				return
			}
			if op == 1 && at == 2 {
				sendFrame(conn, raw(qqObject(qqField("op", 9), qqField("d", false))), 1)
			}
		}
	}))
	defer ws.Close()
	address := "ws" + strings.TrimPrefix(ws.URL, "http") + "/gateway"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release-hello" {
			index := qqInt(raw(r.URL.Query().Get("connection")), 0)
			if index == 0 {
				fmt.Sscan(r.URL.Query().Get("connection"), &index)
			}
			mu.Lock()
			h := hello[index]
			mu.Unlock()
			if h != nil {
				close(h)
			}
			w.WriteHeader(200)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var body any
		if len(data) > 0 {
			_ = json.Unmarshal(data, &body)
		}
		var auth any
		if s := r.Header.Get("Authorization"); s != "" {
			auth = s
		}
		mu.Lock()
		calls = append(calls, qqObject(qqField("url", r.Header.Get("X-Fixture-Logical-URL")), qqField("method", r.Method), qqField("body", body), qqField("authorization", auth)))
		token := r.Header.Get("X-Fixture-Logical-URL") == QQTokenURL
		if token {
			tokens++
		}
		count := tokens
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if token {
			w.Write(raw(qqObject(qqField("access_token", "private-token-"+stringNumber(count)), qqField("expires_in", 7200))))
		} else {
			w.Write(raw(qqObject(qqField("url", address))))
		}
	}))
	defer peer.Close()
	source := object(qqOriginal(t, "native-qq-gateway.fixture.ts", map[string]any{"httpURL": peer.URL, "gatewayURL": address}))
	qqLeafUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return closed == 3 })
	mu.Lock()
	want = raw(qqObject(qqField("frames", frames), protocol.JSONField{Name: "events", Value: source.Get("events")}, qqField("calls", calls), qqField("closed", closed)))
	frames = []protocol.JSONObject{}
	calls = []protocol.JSONObject{}
	connections = 0
	closed = 0
	tokens = 0
	hello = map[int]chan struct{}{}
	mu.Unlock()
	clock := &qqTestTimers{now: 100000000, timers: map[int]struct {
		d time.Duration
		f func()
	}{}}
	client, e := NewQQClient(context.Background(), QQClientOptions{Credentials: qqObject(qqField("appId", " app "), qqField("appSecret", "private-secret")), HTTP: &http.Client{Transport: qqHTTPRoute{peer}}, Now: func() time.Time { return time.UnixMilli(100000000) }, Random: func() float64 { return 0 }, Schedule: clock.schedule})
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway, e := NewQQGateway(QQGatewayOptions{Client: client, Dial: func(ctx context.Context, address string, h http.Header, config *tls.Config) (QQGatewaySocket, error) {
		socket, err := DialQQGatewaySocket(ctx, address, h, config)
		if err == nil {
			mu.Lock()
			ready := hello[connections]
			mu.Unlock()
			if ready != nil {
				close(ready)
			}
		}
		return socket, err
	}, OnReady: func(d json.RawMessage) {
		mu.Lock()
		events = append(events, qqObject(protocol.JSONField{Name: "ready", Value: d}))
		mu.Unlock()
	}, OnResumed: func(d json.RawMessage) {
		mu.Lock()
		events = append(events, qqObject(protocol.JSONField{Name: "resumed", Value: d}))
		mu.Unlock()
	}, OnMessage: func(m protocol.JSONObject) {
		p, e := ExtractQQPayload(m)
		if e != nil {
			t.Error(e)
		}
		mu.Lock()
		events = append(events, qqObject(qqField("message", p)))
		mu.Unlock()
	}})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- gateway.Run(ctx) }()
	qqLeafUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(events) >= 2 })
	clock.fire(t, 1234*time.Millisecond)
	clock.fire(t, time.Second)
	qqLeafUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(events) >= 3 })
	clock.fire(t, 1234*time.Millisecond)
	clock.fire(t, 3*time.Second)
	qqLeafUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(events) >= 4 })
	cancel()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("gateway native close did not join")
	}
	qqLeafUntil(t, func() bool { mu.Lock(); defer mu.Unlock(); return closed == 3 })
	mu.Lock()
	got := qqObject(qqField("frames", frames), qqField("events", events), qqField("calls", calls), qqField("closed", closed))
	mu.Unlock()
	exactJSON(t, raw(got), want, "actual original QQBot gateway")
}

func qqServerFragment(conn net.Conn, data []byte, op byte, final bool) error {
	header := []byte{op}
	if final {
		header[0] |= 0x80
	}
	if len(data) < 126 {
		header = append(header, byte(len(data)))
	} else {
		header = append(header, 126, byte(len(data)>>8), byte(len(data)))
	}
	_, e := conn.Write(append(header, data...))
	return e
}
