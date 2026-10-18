package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestPublicCLISideQuestionControlsRemainIndependentAndJoinOnEOF(t *testing.T) {
	sideEntered, sideCanceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		data, _ := io.ReadAll(r.Body)
		calls.Add(1)
		if bytes.Contains(data, []byte("This is a side question from the user")) {
			close(sideEntered)
			<-r.Context().Done()
			close(sideCanceled)
			return
		}
		response(w, "main", "", nil, "PUBLIC MAIN")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = nil
	delete(o.Environment, "CLAUDE_CODE_SIMPLE")
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
	command, _, e := ParseCommand([]string{"--print", "--model", "fixture-model", "--system-prompt", "Actual side control private prompt", "--thinking", "disabled", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if e != nil {
		t.Fatal(e)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	out := &usageControlOutput{frames: make(chan json.RawMessage, 256)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		code, e := RunCommand(ctx, command, o, reader, out, nil)
		if e == nil && code != 0 {
			e = io.ErrUnexpectedEOF
		}
		done <- e
	}()
	send := func(value any) {
		t.Helper()
		if _, e := writer.Write(append(raw(value), '\n')); e != nil {
			t.Fatal(e)
		}
	}
	wait := func(kind, id string) protocol.JSONObject {
		t.Helper()
		for {
			select {
			case data := <-out.frames:
				row := obj(data)
				if row.String("type") == kind && (id == "" || obj(row.Get("response")).String("request_id") == id) {
					return row
				}
			case e := <-done:
				t.Fatal("CLI ended before", kind, id, e)
			case <-ctx.Done():
				t.Fatal("waiting", kind, id, ctx.Err())
			}
		}
	}
	send(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "seed parent"}})
	wait("result", "")
	send(map[string]any{"type": "control_request", "request_id": "side-one", "request": map[string]any{"subtype": "side_question", "question_id": "11111111-1111-4111-8111-111111111111", "question": "held side", "history": []any{}}})
	select {
	case <-sideEntered:
	case e := <-done:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	send(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "main continues"}})
	wait("result", "")
	send(map[string]any{"type": "control_request", "request_id": "cancel-one", "request": map[string]any{"subtype": "cancel_side_question", "question_id": "11111111-1111-4111-8111-111111111111"}})
	replies := map[string]protocol.JSONObject{}
	for len(replies) < 2 {
		select {
		case data := <-out.frames:
			row := obj(data)
			if row.String("type") == "control_response" {
				reply := obj(row.Get("response"))
				replies[reply.String("request_id")] = reply
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if replies["cancel-one"].String("subtype") != "success" || string(obj(replies["cancel-one"].Get("response")).Get("cancelled")) != "true" {
		t.Fatal(replies)
	}
	if replies["side-one"].String("subtype") != "error" || replies["side-one"].String("error") != "Side question cancelled" {
		t.Fatal("original cancellation output", replies)
	}
	select {
	case <-sideCanceled:
	case <-ctx.Done():
		t.Fatal("native socket escaped cancellation")
	}
	writer.Close()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("EOF failed to close shared CLI", ctx.Err())
	}
	if calls.Load() != 3 {
		t.Fatal("physical model count", calls.Load())
	}
}
