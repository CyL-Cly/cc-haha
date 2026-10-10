package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioninbox"
)

func inboxControl(id string, request any) string {
	data, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": request})
	return string(data) + "\n"
}

func TestActualSessionInboxIdleControlsCancelAndRedeliverWithoutStartingInference(t *testing.T) {
	m := taskMainFixture(t)
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("an idle control started inference without start_if_idle")
		w.WriteHeader(500)
	}))
	defer peer.Close()
	// This instance was already activated by the shared task fixture. The input
	// retains its actual original parsing, owning queue and outbound FIFO.
	m.options.Native.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	request := map[string]any{"subtype": "enqueue_session_message", "message_id": "stop-queued", "sender_session_id": "peer", "text": "pending work"}
	input := inboxControl("enqueue", request) + inboxControl("duplicate", request) + inboxControl("interrupt", map[string]any{"subtype": "interrupt"}) + inboxControl("redeliver", request)
	invalid := map[string]any{"subtype": "enqueue_session_message", "message_id": "invalid", "sender_session_id": "peer", "text": false, "start_if_idle": true}
	input += inboxControl("invalid-start", invalid) + inboxControl("end", map[string]any{"subtype": "end_session"})
	m.options.Input = strings.NewReader(input)
	frames := []protocol.JSONObject{}
	m.options.OnOutput = func(data []byte) error { frames = append(frames, taskObject(t, data)); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	code, e := m.Run(ctx)
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	if requests.Load() != 0 {
		t.Fatal("idle message called the provider", requests.Load())
	}
	if len(frames) != 6 {
		t.Fatal("controls must have one response each and no receipt before consumption", frames)
	}
	for index, want := range []string{"enqueue", "duplicate", "interrupt", "redeliver", "invalid-start", "end"} {
		frame := frames[index]
		response := taskObject(t, frame.Get("response"))
		if frame.String("type") != "control_response" || response.String("request_id") != want {
			t.Fatal(index, frame)
		}
		if want == "invalid-start" {
			if response.String("subtype") != "error" {
				t.Fatal(frame)
			}
			continue
		}
		if response.String("subtype") != "success" {
			t.Fatal(frame)
		}
		if want == "enqueue" || want == "duplicate" || want == "redeliver" {
			payload := taskObject(t, response.Get("response"))
			if payload.String("status") != "queued" || string(payload.Get("duplicate")) != map[bool]string{true: "true", false: "false"}[want == "duplicate"] {
				t.Fatal(frame)
			}
		}
	}
	if len(m.Controller.History()) != 0 {
		t.Fatal("cancelled collaboration input reached history")
	}
}

func TestActualSessionInboxStartIfIdleConsumesAndPersistsThroughCanonicalHarness(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-inbox-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	var requests atomic.Int32
	var body string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		requests.Add(1)
		emitPlanFixture(w, 1, "", "NATIVE_INBOX_OK")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	request := map[string]any{"subtype": "enqueue_session_message", "message_id": "real-inbox-delivery", "sender_session_id": "peer", "text": "Please acknowledge this message", "start_if_idle": true}
	disabled := false
	frames := []protocol.JSONObject{}
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual native inbox", Tools: []string{"Bash,Read,Edit"}, MaxTurns: 2, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(inboxControl("start", request)), Output: io.Discard, Error: io.Discard, OnOutput: func(data []byte) error { frames = append(frames, taskObject(t, data)); return nil }})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, e := m.Run(ctx)
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	if requests.Load() != 1 || !strings.Contains(body, sessioninbox.PromptPrefix[:80]) || !strings.Contains(body, "real-inbox-delivery") {
		t.Fatal("canonical provider input omitted actual channel prompt", requests.Load(), body)
	}
	consumed, result := 0, 0
	for _, frame := range frames {
		if frame.String("type") == "system" && frame.String("subtype") == "session_message_receipt" {
			consumed++
			if frame.String("source_uuid") != sessioninbox.MessageUUID(quoteMain("real-inbox-delivery")) || frame.String("status") != "consumed" || frame.String("session_id") != m.Controller.SessionID() {
				t.Fatal(frame)
			}
		}
		if frame.String("type") == "result" {
			result++
			if frame.String("result") != "NATIVE_INBOX_OK" || string(frame.Get("is_error")) != "false" {
				t.Fatal(frame)
			}
		}
	}
	if consumed != 1 || result != 1 {
		t.Fatal("missing canonical receipt/result", consumed, result, frames)
	}
	persisted := false
	e = filepath.WalkDir(filepath.Join(f.config, "projects"), func(path string, entry fs.DirEntry, failure error) error {
		if failure != nil {
			return failure
		}
		if !entry.IsDir() && entry.Name() == m.Controller.SessionID()+".jsonl" {
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			persisted = strings.Contains(string(data), sessioninbox.MessageUUID(quoteMain("real-inbox-delivery")))
		}
		return nil
	})
	if e != nil || !persisted {
		t.Fatal("actual inbox delivery UUID did not persist", e)
	}
}
