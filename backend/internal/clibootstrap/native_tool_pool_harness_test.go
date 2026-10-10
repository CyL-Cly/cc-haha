package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualMainPrintBareFinalPoolInitAndPhysicalModelOrder(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	var mu sync.Mutex
	var request protocol.JSONObject
	frames := []protocol.JSONObject{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var captured protocol.JSONObject
		_ = captured.UnmarshalJSON(data)
		mu.Lock()
		request = captured
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(event string, value any) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, rawMain(value)) }
		emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "bare-order", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "actual bare order"}})
		emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}})
		emit("message_stop", map[string]string{"type": "message_stop"})
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual bare final tool pool", Tools: []string{"Bash,Read,Edit"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(data []byte) error {
		var value protocol.JSONObject
		if e := value.UnmarshalJSON(data); e != nil {
			return e
		}
		mu.Lock()
		frames = append(frames, value)
		mu.Unlock()
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e := m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("check actual final tool pool")}))})
	if e := m.RunTurn(ctx, input); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"Bash", "Edit", "Read"}
	initObserved := false
	for _, frame := range frames {
		if frame.String("type") == "system" && frame.String("subtype") == "init" {
			var names []string
			_ = json.Unmarshal(frame.Get("tools"), &names)
			if !reflect.DeepEqual(names, want) {
				t.Fatal("actual SDK init final pool differs", names)
			}
			initObserved = true
		}
	}
	var definitions []protocol.JSONObject
	_ = json.Unmarshal(request.Get("tools"), &definitions)
	names := []string{}
	for _, definition := range definitions {
		names = append(names, definition.String("name"))
	}
	if !initObserved || !reflect.DeepEqual(names, want) {
		t.Fatal("actual final pool did not reach init/model", initObserved, names)
	}
}
