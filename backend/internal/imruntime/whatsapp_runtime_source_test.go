package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This fixture sink verifies the original server-message consumer itself; the
// actual encrypted sender and shared Harness are covered by application tests.
func TestWhatsAppActualOriginalServerHandlerPreservesOutputAndState(t *testing.T) {
	rows := []protocol.JSONObject{}
	add := func(value any) { rows = append(rows, object(raw(value))) }
	add(map[string]any{"type": "status", "state": "thinking"})
	add(map[string]any{"type": "status", "state": "thinking"})
	add(map[string]any{"type": "thinking", "text": "private-think"})
	add(map[string]any{"type": "content_start", "blockType": "text"})
	add(map[string]any{"type": "content_delta", "text": "before approval\n"})
	add(map[string]any{"type": "permission_request", "toolName": "Write", "input": map[string]any{"file_path": "/private/fixture", "content": "data"}, "requestId": "p1"})
	add(map[string]any{"type": "permission_resolved", "permissionType": "tool", "requestId": "p1", "allowed": true})
	add(map[string]any{"type": "content_delta", "text": "after approval"})
	add(map[string]any{"type": "message_complete"})
	add(map[string]any{"type": "status", "state": "thinking"})
	add(map[string]any{"type": "content_delta", "text": "retain on ordinary error"})
	add(map[string]any{"type": "error", "message": "private upstream failure"})
	add(map[string]any{"type": "message_complete"})
	add(map[string]any{"type": "tool_result", "toolName": "Read"})
	add(map[string]any{"type": "system_notification", "subtype": "init", "data": map[string]any{"model": "private-model"}})
	var want protocol.JSONObject
	if e := want.UnmarshalJSON(whatsAppOriginal(t, "native-whatsapp-server-handler.fixture.ts", rows)); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notices := []string{}
	chat := NewChatRuntime(ctx, ChatRuntimeOptions{Port: ChatPort{Platform: "whatsapp", SendNotice: func(_ string, text string) error { notices = append(notices, text); return nil }, CreateResponse: func(id string) ResponseStream {
		return &whatsAppSourceResponse{send: func(text string) { notices = append(notices, text) }}
	}}, Now: time.Now})
	r := &WhatsAppRuntime{platformRuntimeCore: &platformRuntimeCore{Chat: chat, ctx: ctx}, thinking: map[string]Text{}, noticed: map[string]bool{}}
	// Use the same handler state branches, with the sender's actual formatting
	// isolated at the existing ChatPort sink instead of a second implementation.
	for _, m := range rows {
		if e := r.handleServerMessage(ctx, "private", m, func(text string) error { notices = append(notices, text); return nil }); e != nil {
			t.Fatal(e)
		}
	}
	chat.Close()
	state := chat.State("private")
	o := protocol.NewJSONObject(protocol.JSONField{Name: "texts", Value: raw(notices)}, protocol.JSONField{Name: "state", Value: raw(map[string]any{"state": state.State, "pendingPermissionCount": state.PendingPermissionCount, "model": state.Model})}, protocol.JSONField{Name: "pending", Value: raw(state.Pending)})
	var a, b any
	_ = json.Unmarshal(raw(o), &a)
	_ = json.Unmarshal(raw(want), &b)
	if !bytes.Equal(raw(a), raw(b)) {
		t.Fatalf("Source server handler mismatch\nGo %s\nSource %s", raw(a), raw(b))
	}
}

type whatsAppSourceResponse struct {
	send func(string)
	text Text
}

func (r *whatsAppSourceResponse) Append(delta Text) error { r.text = r.text.Join(delta); return nil }
func (r *whatsAppSourceResponse) Finish() error {
	if len(r.text) > 0 {
		r.send(FormatWhatsAppOutbound(r.text).String())
		r.text = nil
	}
	return nil
}
