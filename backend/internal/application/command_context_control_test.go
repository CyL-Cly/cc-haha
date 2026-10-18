package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicCLIContextControlsReadSameNativeHistoryWithoutAdditionalInference(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	allowModel := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		calls.Add(1)
		started <- struct{}{}
		select {
		case <-allowModel:
		case <-r.Context().Done():
			return
		}
		response(w, "original-ledger-response", "", nil, "ordinary result")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	command, _, err := ParseCommand([]string{"--print", "--model", "fixture-model", "--tools", "", "--thinking", "disabled", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	out := &usageControlOutput{frames: make(chan json.RawMessage, 256)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		code, err := RunCommand(ctx, command, o, reader, out, nil)
		if err == nil && code != 0 {
			err = io.ErrUnexpectedEOF
		}
		done <- err
	}()
	send := func(data string) {
		t.Helper()
		if _, err := io.WriteString(writer, data+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(kind, id string) json.RawMessage {
		t.Helper()
		for {
			select {
			case data := <-out.frames:
				frame := obj(data)
				if frame.String("type") == kind && (id == "" || obj(frame.Get("response")).String("request_id") == id) {
					return data
				}
			case err := <-done:
				t.Fatal("CLI stopped before control response", err)
			case <-ctx.Done():
				t.Fatal("CLI control timed out", ctx.Err())
			}
		}
	}
	send(`{"type":"control_request","request_id":"before","request":{"subtype":"get_context_usage","estimateOnly":true}}`)
	before := obj(obj(wait("control_response", "before")).Get("response"))
	if before.String("subtype") != "success" || obj(before.Get("response")).String("model") != "fixture-model" || string(obj(before.Get("response")).Get("apiUsage")) != "null" || calls.Load() != 0 {
		t.Fatal("Cold usage started a model", before, calls.Load())
	}
	send(`{"type":"user","uuid":"11111111-1111-4111-8111-111111111111","message":{"role":"user","content":"ordinary instruction"}}`)
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("ordinary request did not start")
	}
	send(`{"type":"control_request","request_id":"running","request":{"subtype":"get_context_usage","estimateOnly":true}}`)
	running := obj(obj(wait("control_response", "running")).Get("response"))
	if running.String("subtype") != "success" || calls.Load() != 1 {
		t.Fatal("live context control cannot inspect this actual running ASK", running, calls.Load())
	}
	close(allowModel)
	_ = wait("result", "")
	send(`{"type":"control_request","request_id":"after","request":{"subtype":"get_context_usage","estimateOnly":true}}`)
	after := obj(obj(wait("control_response", "after")).Get("response"))
	value := obj(after.Get("response"))
	if after.String("subtype") != "success" || string(obj(value.Get("apiUsage")).Get("input_tokens")) == "0" || value.String("model") != "fixture-model" || !bytes.Contains(value.Get("categories"), []byte(`"name":"Messages"`)) || calls.Load() != 1 {
		t.Fatal("Context control did not read actual one-request history", after, calls.Load())
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("CLI did not join after EOF")
	}
	out.mu.Lock()
	data := out.buffer.String()
	out.mu.Unlock()
	if strings.Count(data, `"type":"result"`) != 1 {
		t.Fatal("Context polling created another turn", data)
	}
}
