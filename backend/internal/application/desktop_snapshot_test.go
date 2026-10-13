package application

import (
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

// Source snapshots are a client request/response boundary. Normal completion
// forwards message_complete and any still-pending permission replay; it does
// not emit a reconnect snapshot that would clear those requests in React.
func requestDesktopSessionSync(t *testing.T, socket *websocket.Conn) {
	t.Helper()
	if err := websocket.Message.Send(socket, `{"type":"sync_state"}`); err != nil {
		t.Fatal("actual client sync_state failed", err)
	}
}

// sync_state produces one authoritative response. After completion or Stop,
// a running response is a regression rather than a reason to await another frame.
func assertDesktopIdleSnapshot(t *testing.T, snapshot protocol.JSONObject) {
	t.Helper()
	if snapshot.String("type") != "session_state" || snapshot.String("turnState") != "idle" {
		t.Fatal("actual explicit sync_state did not report idle foreground", string(raw(snapshot)))
	}
}
