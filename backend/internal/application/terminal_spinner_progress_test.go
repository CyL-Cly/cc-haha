package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSessionSpinnerEventsSameHarnessUTF16NextHumanAndClosedSink(t *testing.T) {
	var requests atomic.Int32
	toolInput := map[string]any{"taskId": "99", "status": "in_progress"}
	firstText, nextText := "ACTUAL_SPINNER_😀_DONE", "ACTUAL_NEXT_SPINNER_DONE"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		switch requests.Add(1) {
		case 1:
			response(w, "spinner-tool", "TaskUpdate", toolInput, "")
		case 2:
			if !bytes.Contains(body, []byte("Task not found")) {
				t.Error("Actual tool result missing from the same Harness", string(body))
			}
			response(w, "spinner-first", "", nil, firstText)
		case 3:
			if !bytes.Contains(body, []byte("ACTUAL_SPINNER_NEXT_HUMAN")) || !bytes.Contains(body, []byte("spinner-tool")) || bytes.Contains(body, []byte("terminal_spinner")) {
				t.Error("Internal Spinner observation changed canonical next-Human history", string(body))
			}
			response(w, "spinner-next", "", nil, nextText)
		default:
			t.Error("Spinner observation invoked an extra model", string(body))
		}
	}))
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.Tools = []string{"TaskUpdate"}
	var wire bytes.Buffer
	o.OnSDK = func(value json.RawMessage) error { _, err := wire.Write(value); return err }
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var eventMu sync.Mutex
	var events []tui.Event
	emit := func(event tui.Event) error {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
		return nil
	}
	if err = s.Submit(ctx, tui.Input{TurnID: "actual-spinner-first", Mode: "prompt", Text: "Actual interactive Spinner tool"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(ctx, tui.Input{TurnID: "actual-spinner-next", Mode: "prompt", Text: "ACTUAL_SPINNER_NEXT_HUMAN"}, emit); err != nil {
		t.Fatal(err)
	}
	eventMu.Lock()
	observed := append([]tui.Event(nil), events...)
	eventMu.Unlock()
	starts, units := 0, 0
	turns := map[string]bool{}
	for _, event := range observed {
		if event.Kind != tui.TerminalSpinnerProgressChanged {
			continue
		}
		if event.Spinner == nil || event.Background {
			t.Fatal("Actual parent progress lost its own immutable turn carrier", event)
		}
		turns[event.TurnID] = true
		units += event.Spinner.AddedResponseUnits
		if event.Spinner.Mode == "requesting" {
			starts++
		}
	}
	wantUnits := len(raw(toolInput)) + len(utf16.Encode([]rune(firstText))) + len(utf16.Encode([]rune(nextText)))
	if starts != 3 || units != wantUnits || !turns["actual-spinner-first"] || !turns["actual-spinner-next"] || requests.Load() != 3 || bytes.Contains(wire.Bytes(), []byte("terminal_spinner")) {
		t.Fatalf("Actual parent Spinner projection: starts%d units%d/%d turns%v HTTP%d wire%s", starts, units, wantUnits, turns, requests.Load(), wire.String())
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	before := len(events)
	if err = s.nativeTerminalSpinner(tui.TerminalSpinnerProgress{Mode: "requesting", AddedResponseUnits: 99}); err != nil || len(events) != before {
		t.Fatal("Closed Session invoked its retired terminal sink", err)
	}
}
