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
	"sync/atomic"
	"testing"
	"time"
)

// The actual ASK executes Read and consumes its result in its next physical
// model request. Count endpoints are deliberately separate from Messages:
// Source Read never turns a count failure into a Haiku sampling request.
func TestActualMainPrintLargeReadCountsAndFallbackWithoutSampling(t *testing.T) {
	for _, scenario := range []struct {
		name, extension, response, expectedError string
		status, counts, length                   int
	}{
		{name: "small-no-count", extension: "txt", response: `{"input_tokens":99999}`, length: 40},
		{name: "large-api-success", extension: "txt", response: `{"input_tokens":91}`, counts: 1, length: 28000},
		{name: "large-null-estimate", extension: "txt", response: `{"input_tokens":null}`, counts: 1, length: 28000},
		{name: "large-string-estimate", extension: "txt", response: `{"input_tokens":"90000"}`, counts: 1, length: 28000},
		{name: "unsupported-count-estimate", extension: "txt", status: 404, counts: 1, length: 28000},
		{name: "retry-once-then-estimate", extension: "txt", status: 500, counts: 2, length: 28000},
		{name: "fractional-real-limit", extension: "txt", response: `{"input_tokens":25000.5}`, counts: 1, length: 28000, expectedError: "File content (25000.5 tokens) exceeds maximum allowed tokens (25000). Use offset and limit parameters to read specific portions of the file, or search for specific content instead of reading the whole file."},
		{name: "json-dense-estimate-limit", extension: "jsonl", status: 404, counts: 1, length: 52000, expectedError: "File content (26000 tokens) exceeds maximum allowed tokens (25000). Use offset and limit parameters to read specific portions of the file, or search for specific content instead of reading the whole file."},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var path string
			content := strings.Repeat("x", scenario.length)
			var counts, messages atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if r.URL.Path == "/v1/messages/count_tokens" {
					counts.Add(1)
					var payload struct {
						Model    string
						Messages []struct{ Role, Content string }
						Tools    []json.RawMessage
					}
					if err := json.Unmarshal(body, &payload); err != nil || len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Messages[0].Content != content || len(payload.Tools) != 0 || payload.Model != "fixture-model" {
						t.Error("Read count is not raw Source user content/main model", err)
					}
					if scenario.status != 0 {
						http.Error(w, `{"type":"error","error":{"type":"api_error","message":"unsupported count"}}`, scenario.status)
						return
					}
					w.Header().Set("content-type", "application/json")
					io.WriteString(w, scenario.response)
					return
				}
				if r.URL.Path != "/v1/messages" {
					t.Error("unexpected endpoint", r.URL.Path)
				}
				if round := messages.Add(1); round == 1 {
					emitNativeTaskTool(w, "actual-large-read", "Read", map[string]any{"file_path": path})
					return
				} else if round != 2 {
					t.Error("Read triggered sampling or extra ASK", round)
				}
				if scenario.expectedError != "" {
					if !bytes.Contains(body, rawMain(scenario.expectedError)[1:len(rawMain(scenario.expectedError))-1]) {
						t.Error("Source exact limit error missing from next real request")
					}
				} else if !bytes.Contains(body, []byte(content)) {
					t.Error("large Read did not reach the next actual request")
				}
				if bytes.Contains(body, []byte("capability is not migrated")) {
					t.Error("large Read retains capability failure")
				}
				emitPlanFixture(w, 2, "", "ACTUAL_LARGE_READ_RECOVERED")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
			path = filepath.Join(m.options.Native.CWD, "actual-history."+scenario.extension)
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Read the actual private history."}}`)); err != nil {
				t.Fatal(err)
			}
			if counts.Load() != int32(scenario.counts) || messages.Load() != 2 {
				t.Fatal("physical request cardinality", counts.Load(), messages.Load())
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != content {
				t.Fatal("Read mutated its input", err)
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
