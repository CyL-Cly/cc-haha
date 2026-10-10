package imruntime

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestWecomOriginalHTTPTimeoutPhaseAndCloseJoinsPhysicalBody(t *testing.T) {
	for _, phase := range []string{"before-headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			ended := make(chan struct{}, 4)
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() { ended <- struct{}{} }()
				if phase == "body" {
					w.Header().Set("Content-Length", "4")
					_, _ = w.Write([]byte("a"))
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer peer.Close()
			ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}))
			want := runWecomFixture(t, "wecom-timeout.fixture.ts", map[string]any{"url": peer.URL, "ca": ca, "timeoutMs": 80})
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Fatal("original HTTP socket did not close")
			}
			timeout := 80 * time.Millisecond
			c, e := NewWecomClient(context.Background(), WecomClientOptions{HTTP: peer.Client(), RequestTimeout: &timeout})
			if e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { _, e := c.DownloadFile(context.Background(), peer.URL, nil); done <- e }()
			observed := wecomJSONObject()
			select {
			case e := <-done:
				if e != nil {
					observed.Set("error", raw(e.Error()))
				} else {
					t.Fatal("partial response unexpectedly completed")
				}
			case <-time.After(200 * time.Millisecond):
				observed.Set("pending", raw(true))
			}
			exactJSON(t, raw(wecomJSONObject(wecomField("observed", observed))), raw(want), "timeout phase")
			closed := make(chan struct{})
			go func() { c.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("Close did not join body read")
			}
			if string(observed.Get("pending")) == "true" {
				select {
				case e := <-done:
					if e == nil {
						t.Fatal("closed body returned success")
					}
				case <-time.After(time.Second):
					t.Fatal("body read escaped Close")
				}
			}
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Fatal("Go HTTP socket did not close")
			}
		})
	}
}

func TestWecomCloseCancelsActualBlockedWebSocketWriteAndQueuedCall(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	c, e := NewWecomClient(context.Background(), WecomClientOptions{})
	if e != nil {
		t.Fatal(e)
	}
	c.mu.Lock()
	c.socket = &Socket{conn: left}
	c.mu.Unlock()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, e := c.SendReply(context.Background(), NewText("same"), wecomJSONObject(wecomField("content", strings.Repeat("x", 1<<20))), "")
		first <- e
	}()
	// Observe the actual masked frame header before withholding body reads.
	if _, e := io.ReadFull(right, make([]byte, 14)); e != nil {
		t.Fatal(e)
	}
	go func() {
		_, e := c.SendReply(context.Background(), NewText("same"), wecomJSONObject(wecomField("content", "queued")), "")
		second <- e
	}()
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited behind blocked native Write")
	}
	for _, done := range []chan error{first, second} {
		select {
		case e := <-done:
			if e == nil {
				t.Fatal("cancelled write/queued call succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("reply escaped Close")
		}
	}
}

func TestWecomCredentialStringifyHasOriginalBinary64AndUTF16Boundary(t *testing.T) {
	values := []json.RawMessage{}
	for _, value := range []string{`1e400`, `-1e400`, `9007199254740993`, `-0`, `"value\ud800"`, `true`, `{"nested":1e400}`} {
		values = append(values, json.RawMessage(value))
	}
	want := runWecomSource(t, map[string]any{"credentials": values})
	got := []protocol.JSONObject{}
	for _, value := range values {
		frame := wecomFrame("aibot_subscribe", NewText("aibot_subscribe_fixed"), nil)
		frame.Set("body", raw(wecomJSONObject(protocol.JSONField{Name: "bot_id", Value: []byte(value)}, wecomField("secret", true))))
		wire := wecomWire(frame)
		got = append(got, object(object(wire).Get("body")))
	}
	exactJSON(t, raw(got), want.Get("credentials"), "credential wire")
}
