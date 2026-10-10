package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func emitInternalReadFixture(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(event string, value any) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, rawMain(value)) }
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "internal-read-576", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}})
	emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "read-internal-576", "name": "Read", "input": map[string]any{}}})
	emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": string(rawMain(map[string]string{"file_path": path}))}})
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 1}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

// Ordinary defaults and the actual taskstore producer reproduce the installed
// CLI failure: a model Read must receive the real private task JSON without an
// allow rule, custom system prompt, tool subset or bypass permission mode.
func TestActualDefaultMainPrintReadsTaskStoreJSONAndColdResumedSameSID(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-internal-read-576"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	sid := "57700000-0000-4000-8000-000000000576"
	store, err := taskstore.New(taskstore.Options{ConfigDir: f.config, PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(sid, protocol.NewJSONObject(protocol.JSONField{Name: "subject", Value: quoteMain("PRIVATE_TASK_READ_576")}, protocol.JSONField{Name: "status", Value: quoteMain("completed")}, protocol.JSONField{Name: "description", Value: quoteMain("actual taskstore producer")}, protocol.JSONField{Name: "blocks", Value: rawMain([]string{})}, protocol.JSONField{Name: "blockedBy", Value: rawMain([]string{})}))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.config, "tasks", sid, "1.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := 0
	readResults := []protocol.JSONObject{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var call struct{ Messages []protocol.JSONObject }
		if e := json.Unmarshal(body, &call); e != nil {
			t.Error(e)
		}
		mu.Lock()
		requests++
		index := requests
		mu.Unlock()
		if index%2 == 1 {
			emitInternalReadFixture(w, path)
			return
		}
		var result protocol.JSONObject
		for _, message := range call.Messages {
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(message.Get("content"), &blocks)
			for _, block := range blocks {
				if block.String("type") == "tool_result" && block.String("tool_use_id") == "read-internal-576" {
					result = block
				}
			}
		}
		mu.Lock()
		readResults = append(readResults, result.Clone())
		mu.Unlock()
		text := "REFUSED_NO_TASK_CONTENT_576"
		if string(result.Get("is_error")) != "true" && strings.Contains(result.String("content"), "PRIVATE_TASK_READ_576") {
			text = "TASK_INTERNAL_READ_COMPLETED_576"
		}
		emitPlanFixture(w, index, "", text)
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	for _, resumed := range []bool{false, true} {
		arguments := PrintArguments{Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}
		if resumed {
			arguments.Resume = sid
		} else {
			arguments.SessionID = sid
		}
		var output strings.Builder
		m, e := NewMainPrint(MainPrintOptions{Arguments: arguments, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(line []byte) error { output.Write(line); return nil }})
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		message := protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("Read the actual task JSON and confirm its subject.")})
		user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(sid)}, protocol.JSONField{Name: "message", Value: rawMain(message)})
		if e = m.StartSession(ctx); e == nil {
			e = m.RunTurn(ctx, user)
		}
		closeErr := m.ShutdownSession(ctx)
		cancel()
		if e != nil || closeErr != nil {
			t.Fatalf("resumed=%v run=%v close=%v", resumed, e, closeErr)
		}
		mu.Lock()
		got := readResults[len(readResults)-1].Clone()
		mu.Unlock()
		if string(got.Get("is_error")) == "true" || !strings.Contains(got.String("content"), "PRIVATE_TASK_READ_576") {
			t.Fatalf("resumed=%v default real Read did not reach model content: %s; frames %s", resumed, rawMain(got), output.String())
		}
		if !strings.Contains(output.String(), "TASK_INTERNAL_READ_COMPLETED_576") {
			t.Fatal("actual completion absent", output.String())
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("Read mutated original task JSON")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 4 || len(readResults) != 2 {
		t.Fatal("actual model rounds", requests, len(readResults))
	}
}

func TestActualDefaultMainPrintNilASKRetainsOriginalModelVisibleReadMessage(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	path := filepath.Join(f.home, "outside-readable.txt")
	f.write(t, path, "PRIVATE_MUST_NOT_READ_576")
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-ask-message-576"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	expected := "Claude requested permissions to read from " + path + ", but you haven't granted it yet."
	var mu sync.Mutex
	count := 0
	var got protocol.JSONObject
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		count++
		index := count
		mu.Unlock()
		if index == 1 {
			emitInternalReadFixture(w, path)
			return
		}
		var input struct{ Messages []protocol.JSONObject }
		_ = json.Unmarshal(body, &input)
		for _, message := range input.Messages {
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(message.Get("content"), &blocks)
			for _, block := range blocks {
				if block.String("tool_use_id") == "read-internal-576" {
					mu.Lock()
					got = block.Clone()
					mu.Unlock()
				}
			}
		}
		emitPlanFixture(w, index, "", "ASK_MESSAGE_RETAINED_576")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{Model: "fixture-model", MaxTurns: 100400, PermissionMode: "default", Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Read the external file if permitted."})})
	if e = m.RunTurn(ctx, user); e != nil {
		t.Fatal(e)
	}
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 2 || string(got.Get("is_error")) != "true" || got.String("content") != expected {
		t.Fatalf("actual nil resolver model receipt %s count%d expected %q", rawMain(got), count, expected)
	}
	if strings.Contains(string(rawMain(got)), "PRIVATE_MUST_NOT_READ_576") || strings.Contains(string(rawMain(got)), "undefined") {
		t.Fatal("ASK executed or lost source message", rawMain(got))
	}
}
