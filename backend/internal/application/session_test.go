package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func nativeSessionOptions(t *testing.T, baseURL string) Options {
	t.Helper()
	root := t.TempDir()
	home, work, managed := filepath.Join(root, "home"), filepath.Join(root, "work"), filepath.Join(root, "managed")
	for _, p := range []string{home, work, managed} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "ANTHROPIC_BASE_URL": baseURL, "ANTHROPIC_API_KEY": "fake-only-loopback", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "PATH": "/usr/bin:/bin", "TZ": "UTC"}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: work, Platform: "darwin", ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	// A private HOME does not isolate macOS Keychain. The shared fixture must
	// substitute that OS primitive even when a later account/status consumer
	// probes credentials while the ordinary model uses a loopback API key.
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	// The real CLI init also performs an unauthenticated HEAD. A private HOME
	// cannot authorize a fixture with an absent base URL to contact the public
	// API, so only the declared loopback transport may execute that primitive.
	native.PreconnectHTTP = func(request *http.Request, actual *http.Client) (*http.Response, error) {
		allowed := func(request *http.Request) bool {
			return request != nil && request.Method == http.MethodHead && request.URL != nil && request.URL.Hostname() == "127.0.0.1" && request.URL.Port() != "" && request.URL.User == nil && (request.URL.Scheme == "http" || request.URL.Scheme == "https")
		}
		if actual == nil || !allowed(request) {
			return nil, fmt.Errorf("isolated fixture preconnect requires exact loopback HEAD")
		}
		client := *actual
		client.CheckRedirect = func(next *http.Request, previous []*http.Request) error {
			if !allowed(next) {
				return fmt.Errorf("isolated fixture preconnect redirect escapes loopback")
			}
			if actual.CheckRedirect != nil {
				return actual.CheckRedirect(next, previous)
			}
			return nil
		}
		return client.Do(request)
	}
	// A real Source child observes the canonical process.cwd() directory.
	// Persist fixtures in that same namespace instead of a /var alias which
	// native startup correctly resolves to /private/var before resume.
	return Options{Arguments: clibootstrap.PrintArguments{Model: "fixture-model", SystemPrompt: "Use the actual tools to execute the requested test.", SessionID: "fe210ac4-5364-4b5e-a0b8-e351b70e0a17", Tools: []string{"Read,Write,Edit,Bash,Glob,Grep"}, MaxTurns: 20, PermissionMode: "bypassPermissions", Sources: clibootstrap.DefaultState()}, Environment: env, Native: native, WorkDir: native.CWD, Version: "999.0.0-application-test", Platform: "darwin"}
}

func response(w http.ResponseWriter, id, tool string, input any, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(kind string, value any) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw(value)) }
	usage := map[string]any{"input_tokens": 12, "output_tokens": 1, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": usage}})
	stop := "end_turn"
	if tool != "" {
		stop = "tool_use"
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "call-" + id, "name": tool, "input": map[string]any{}}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(raw(input))}})
	} else {
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}})
	}
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 8}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

func TestDefaultSystemPromptAndActualMemoryReachSharedHarness(t *testing.T) {
	var captured json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		response(w, "default-prompt", "", nil, "DEFAULT PROMPT OK")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.SystemPrompt = ""
	if err := os.WriteFile(filepath.Join(o.WorkDir, "AGENTS.md"), []byte("Use actual project instructions.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"language":"中文"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Text: "Use the default prompt."}, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are an interactive agent", "Always respond in 中文", "Use actual project instructions.", "# auto memory"} {
		if !bytes.Contains(captured, []byte(want)) {
			t.Fatalf("default context %q did not reach actual model request: %s", want, captured)
		}
	}
	if bytes.Contains(captured, []byte("__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__")) {
		t.Fatal("third-party origin inherited Anthropic global-cache prompt boundary")
	}
}

func TestSharedSessionRunsRealReadWriteBashEditAndResumesSameHistory(t *testing.T) {
	var mu sync.Mutex
	requests := []json.RawMessage{}
	var file string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected native transport %s %s", r.Method, r.URL)
			http.Error(w, "bad", 400)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, append(json.RawMessage(nil), body...))
		ordinal := len(requests)
		mu.Unlock()
		switch ordinal {
		case 1:
			response(w, "write", "Write", map[string]any{"file_path": file, "content": "before\n"}, "")
		case 2:
			if data, err := os.ReadFile(file); err != nil || string(data) != "before\n" {
				t.Errorf("model advanced before real Write %s %v", data, err)
			}
			response(w, "read", "Read", map[string]any{"file_path": file}, "")
		case 3:
			response(w, "edit", "Edit", map[string]any{"file_path": file, "old_string": "before", "new_string": "after"}, "")
		case 4:
			response(w, "bash", "Bash", map[string]any{"command": "cat notes.txt"}, "")
		case 5:
			if !bytes.Contains(body, []byte("after")) {
				t.Errorf("actual tool results missing from next request")
			}
			response(w, "done", "", nil, "DONE")
		case 6:
			if !bytes.Contains(body, []byte("DONE")) || !bytes.Contains(body, []byte("call-write")) {
				t.Errorf("restart lost prior shared transcript")
			}
			response(w, "resume", "", nil, "RESUMED")
		default:
			t.Errorf("unexpected model call %d", ordinal)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	file = filepath.Join(o.WorkDir, "notes.txt")
	var stderr bytes.Buffer
	o.Stderr = &stderr
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	events := []tui.Event{}
	if err = s.Submit(ctx, tui.Input{TurnID: "first", Text: "Execute all native tools."}, func(e tui.Event) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err, stderr.String())
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "after\n" {
		t.Fatal("real file result", string(data), err)
	}
	text := ""
	tools := map[string]bool{}
	for _, e := range events {
		if e.Kind == tui.TextDelta {
			text += e.Text
		}
		if e.Kind == tui.ToolStarted {
			tools[e.ToolName] = true
		}
	}
	if text != "DONE" || len(tools) != 4 {
		t.Fatal("streamed output or native tools missing", text, tools)
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	o.Arguments.Resume = o.Arguments.SessionID
	o.Arguments.SessionID = ""
	s, err = NewSession(ctx, o)
	if err != nil {
		t.Fatal("restart", err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(ctx, tui.Input{TurnID: "second", Text: "Continue the same session."}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 6 {
		t.Fatal("shared query loop requests", len(requests))
	}
	var paths []string
	_ = filepath.Walk(filepath.Join(o.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(p string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() && strings.HasSuffix(p, ".jsonl") {
			paths = append(paths, p)
		}
		return nil
	})
	if len(paths) != 1 {
		t.Fatal("expected same SID JSONL", paths)
	}
}

func TestPermissionWaitUsesOwnerAndUpdatedInputWithActualNativeWrite(t *testing.T) {
	var original string
	var posts atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if posts.Add(1) == 1 {
			response(w, "permission", "Write", map[string]any{"file_path": original, "content": "original"}, "")
		} else {
			response(w, "complete", "", nil, "APPROVED")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.PermissionMode = "default"
	original = filepath.Join(o.WorkDir, "original.txt")
	updated := filepath.Join(o.WorkDir, "approved.txt")
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	asked := make(chan tui.PermissionRequest, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.Submit(ctx, tui.Input{TurnID: "approval", Text: "Write with approval."}, func(e tui.Event) error {
			if e.Kind == tui.PermissionAsked {
				asked <- *e.Permission
			}
			return nil
		})
	}()
	var request tui.PermissionRequest
	select {
	case request = <-asked:
	case err := <-done:
		t.Fatalf("no permission asked: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = s.RespondPermission(ctx, request.ID, tui.PermissionDecision{Allowed: true, UpdatedInput: raw(map[string]any{"file_path": updated, "content": "approved"})}); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("unapproved input executed", err)
	}
	if data, err := os.ReadFile(updated); err != nil || string(data) != "approved" {
		t.Fatal("updated input not executed", string(data), err)
	}
}
