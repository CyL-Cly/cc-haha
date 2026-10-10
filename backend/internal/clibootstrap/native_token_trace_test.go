package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualCountTraceCompletesAfterInternalRequestCleanup(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Errorf("unexpected inference %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"input_tokens":37}`)
	}))
	defer peer.Close()
	f.n.Environment["CC_HAHA_TRACE_API_CALLS"] = "1"
	f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-counter-trace-key"
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ENABLE_TOOL_SEARCH"] = "false"
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{Model: "fixture-model", SystemPrompt: "Actual trace completion regression", Tools: []string{"Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ReadContextUsage(ctx, false); err != nil {
		t.Fatal(err)
	}
	if m.options.Trace == nil {
		t.Fatal("actual counter has no trace service")
	}
	if err = m.options.Trace.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.config, "cc-haha", "traces", m.Controller.SessionID()+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	latest := map[string]protocol.JSONObject{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var row protocol.JSONObject
		if err = row.UnmarshalJSON(line); err != nil {
			t.Fatal(err)
		}
		if row.String("type") != "call" {
			continue
		}
		var call protocol.JSONObject
		_ = json.Unmarshal(row.Get("record"), &call)
		latest[call.String("id")] = call
	}
	if len(latest) == 0 {
		t.Fatal("actual physical counters not captured")
	}
	for _, call := range latest {
		if call.String("querySource") != "count_tokens" || call.String("status") != "ok" {
			t.Fatalf("completed count became false abort: status=%s error=%s", call.String("status"), call.Get("error"))
		}
	}
	if bytes.Contains(data, []byte("offline-counter-trace-key")) {
		t.Fatal("trace persisted actual auth credential")
	}
}
