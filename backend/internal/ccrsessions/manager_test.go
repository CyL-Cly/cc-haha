package ccrsessions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

func TestRemoteManagerRealSubscriptionEventsControlsAndCloseCancelHeldHTTP(t *testing.T) {
	var mu sync.Mutex
	var wire protocol.JSONObject
	var conn *websocket.Conn
	connected := make(chan struct{})
	controls := make(chan protocol.JSONObject, 4)
	held := make(chan struct{})
	cancelled := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owned-oauth" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("real cached credential/version", r.Header)
		}
		if strings.Contains(r.URL.Path, "/subscribe") {
			if r.URL.Path != "/v1/sessions/ws/session_owned/subscribe" || r.URL.Query().Get("organization_uuid") != "owned-org" {
				t.Error("actual subscription URL", r.URL)
			}
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			mu.Lock()
			conn = c
			mu.Unlock()
			close(connected)
			_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"future_sdk","actual":true}`))
			for {
				_, data, err := c.Read(r.Context())
				if err != nil {
					return
				}
				controls <- objectJSON(string(data))
			}
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/v1/sessions/session_owned/events" || r.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" || r.Header.Get("x-organization-uuid") != "owned-org" {
			t.Error("actual event transport", r.Method, r.URL, r.Header)
		}
		mu.Lock()
		wire = objectJSON(string(body))
		mu.Unlock()
		if strings.Contains(string(body), "HELD_MANAGER_HTTP") {
			close(held)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		ccrJSON(w, map[string]any{})
	}))
	defer peer.Close()
	s := ccrFixture(t, peer)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	message := make(chan protocol.JSONObject, 1)
	m, err := s.NewRemoteManager(ctx, "session_owned", ManagerCallbacks{Message: func(frame protocol.JSONObject) { message <- frame }})
	if err != nil {
		t.Fatal(err)
	}
	m.Start()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("actual socket did not connect")
	}
	select {
	case value := <-message:
		if value.String("type") != "future_sdk" {
			t.Fatal(value)
		}
	case <-ctx.Done():
		t.Fatal("future SDK type dropped")
	}
	if ok, err := m.SendMessage(ctx, json.RawMessage(`"ACTUAL_MANAGER_HTTP"`), "owned-human"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	mu.Lock()
	events := wire.Get("events")
	mu.Unlock()
	if !strings.Contains(string(events), `"uuid":"owned-human"`) || !strings.Contains(string(events), `"parent_tool_use_id":null`) {
		t.Fatal(string(events))
	}
	if err := m.QueueControl(objectJSON(`{"type":"control_response","response":{"subtype":"success","request_id":"owned-permission","response":{"behavior":"allow","updatedInput":{"actual":true}}}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case control := <-controls:
		if control.String("type") != "control_response" {
			t.Fatal(control)
		}
	case <-ctx.Done():
		t.Fatal("actual permission response absent")
	}
	if err := m.QueueControl(objectJSON(`{"type":"control_request","request_id":"owned-interrupt","request":{"subtype":"interrupt"}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case control := <-controls:
		if request, _ := control.Object("request"); request.String("subtype") != "interrupt" {
			t.Fatal(control)
		}
	case <-ctx.Done():
		t.Fatal("actual interrupt absent")
	}
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		_, _ = m.SendMessage(ctx, json.RawMessage(`"HELD_MANAGER_HTTP"`), "held-human")
	}()
	select {
	case <-held:
	case <-ctx.Done():
		t.Fatal("held real HTTP did not arrive")
	}
	m.Close()
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("close did not cancel actual HTTP")
	}
	select {
	case <-sendDone:
	default:
		t.Fatal("close returned before owned HTTP worker joined")
	}
	if _, err := m.SendMessage(ctx, json.RawMessage(`"after close"`), "late"); err == nil {
		t.Fatal("closed manager accepted a new event")
	}
	mu.Lock()
	_ = conn.CloseNow()
	mu.Unlock()
}
