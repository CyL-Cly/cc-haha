package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualDesktopFactoryRetainsOriginalReplayRegardlessOfCLIFlagDefault(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response(w, "desktop-replay", "", nil, "ACTUAL_DESKTOP_REPLAY_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	if o.Arguments.Replay {
		t.Fatal("fixture must begin with the ordinary CLI default")
	}
	var mu sync.Mutex
	var messages []protocol.JSONObject
	live, err := DesktopFactory(o)(context.Background(), server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir, OnSDK: func(data json.RawMessage) {
		mu.Lock()
		messages = append(messages, obj(data))
		mu.Unlock()
	}}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(context.Background())
	desktop := live.(*DesktopSession)
	if err := desktop.engine.Submit(context.Background(), tui.Input{Text: "original desktop replay"}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	acknowledgments := 0
	for _, message := range messages {
		if message.String("type") == "user" && string(message.Get("isReplay")) == "true" {
			acknowledgments++
			body, _ := message.Object("message")
			if body.String("content") != "original desktop replay" || message.String("session_id") != o.Arguments.SessionID || message.String("uuid") == "" {
				t.Fatal("desktop SDK acknowledgment lost original identity", message)
			}
		}
	}
	if acknowledgments != 1 {
		t.Fatalf("original desktop enables replay even when CLI defaults off: got %d acknowledgments", acknowledgments)
	}
}
