package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type usageControlOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	lines  bytes.Buffer
	frames chan json.RawMessage
}

func (w *usageControlOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.buffer.Write(p)
	_, _ = w.lines.Write(p)
	for {
		data := w.lines.Bytes()
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			break
		}
		line := append(json.RawMessage(nil), data[:end]...)
		w.lines.Next(end + 1)
		if json.Valid(line) {
			w.frames <- line
		}
	}
	return len(p), nil
}

func TestPublicCLIUsageControlsReadSharedLedgerWithoutAdditionalModelOrTranscriptTurns(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
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
	send(`{"type":"control_request","request_id":"before","request":{"subtype":"get_session_usage"}}`)
	before := obj(obj(wait("control_response", "before")).Get("response"))
	if before.String("subtype") != "success" || string(obj(before.Get("response")).Get("totalInputTokens")) != "0" || calls.Load() != 0 {
		t.Fatal("Cold usage started a model", before, calls.Load())
	}
	send(`{"type":"user","uuid":"11111111-1111-4111-8111-111111111111","message":{"role":"user","content":"ordinary instruction"}}`)
	_ = wait("result", "")
	send(`{"type":"control_request","request_id":"after","request":{"subtype":"get_session_usage"}}`)
	after := obj(obj(wait("control_response", "after")).Get("response"))
	value := obj(after.Get("response"))
	if after.String("subtype") != "success" || string(value.Get("totalInputTokens")) == "0" || !bytes.Contains(value.Get("models"), []byte(`"model":"fixture-model"`)) || calls.Load() != 1 {
		t.Fatal("Control did not read actual one-request ledger", after, calls.Load())
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
		t.Fatal("Usage polling created another turn", data)
	}
}
