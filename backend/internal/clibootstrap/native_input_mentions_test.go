package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestOrdinaryAndDequeuedInputMentionsUseActualCallerTextOnly(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	f.write(t, filepath.Join(f.cwd, "context.txt"), "actual last-block file context")
	f.write(t, filepath.Join(f.cwd, "preceding.txt"), "THIS PRECEDING FILE MUST NOT BECOME CONTEXT")
	f.write(t, filepath.Join(f.cwd, "assistant.txt"), "THIS ASSISTANT FILE MUST NOT BECOME CONTEXT")
	f.write(t, filepath.Join(f.cwd, "queued.txt"), "actual queued caller file context")
	var mu sync.Mutex
	requests := []string{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(data))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"normal-input\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Assistant data mentions @assistant.txt\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual normal and queued caller discovery", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	user := func(content []byte) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: content}))})
	}
	if e = m.RunTurn(ctx, user([]byte(`[{"type":"text","text":"preceding @preceding.txt"},{"type":"text","text":"actual user @context.txt"}]`))); e != nil {
		t.Fatal(e)
	}
	// Use the real parsed SDK admission/dequeue path. This tests the queued
	// caller's own prompt rather than manufacturing an attachment or ASK.
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, e := m.Host.AdmitParsedSDK(scope, user(quoteMain("queued user @queued.txt")))
		if e != nil {
			return e
		}
		_, e = future.Await(scope)
		if e != nil {
			return e
		}
		ran, e := m.Host.RunDequeued(scope, ctx)
		if e == nil && !ran {
			return fmt.Errorf("actual SDK dequeued user did not run")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || !strings.Contains(requests[0], "actual last-block file context") || !strings.Contains(requests[1], "actual queued caller file context") {
		t.Fatal("ordinary/dequeued actual model requests omitted caller attachments", len(requests))
	}
	for _, request := range requests {
		if strings.Contains(request, "THIS PRECEDING FILE MUST") || strings.Contains(request, "THIS ASSISTANT FILE MUST") {
			t.Fatal("non-caller text became an attachment discovery signal")
		}
	}
}
