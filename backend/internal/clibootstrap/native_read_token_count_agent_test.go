package clibootstrap

import (
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

func TestActualAgentReadCounterUsesLiveRootModelWithoutExtraAgentOrParentMutation(t *testing.T) {
	var path string
	var roots, children, counts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model    string
			Messages json.RawMessage
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		if r.URL.Path == "/v1/messages/count_tokens" {
			counts.Add(1)
			if payload.Model != "fixture-model" {
				t.Error("Agent-specific model replaced Source global main-loop count model", payload.Model)
			}
			w.Header().Set("content-type", "application/json")
			io.WriteString(w, `{"input_tokens":37}`)
			return
		}
		if payload.Model == "child-count-model" {
			if children.Add(1) == 1 {
				emitNativeTaskTool(w, "actual-child-read-count", "Read", map[string]any{"file_path": path})
			} else {
				if !strings.Contains(string(payload.Messages), "CHILD_PRIVATE_CONTENT") {
					t.Error("Read count result did not reach actual child query")
				}
				emitPlanFixture(w, 2, "", "CHILD_COUNT_DONE")
			}
			return
		}
		if roots.Add(1) == 1 {
			emitNativeTaskTool(w, "actual-root-agent-count", "Agent", map[string]any{"subagent_type": "general-purpose", "description": "actual child Read count", "prompt": "Read the private long history and report completion.", "model": "haiku"})
		} else {
			emitPlanFixture(w, 4, "", "ROOT_COUNT_DONE")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "child-count-model" }, nil)
	path = filepath.Join(m.options.Native.CWD, "child-long-history.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("CHILD_PRIVATE_CONTENT", 1500)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Delegate the private history read to the haiku agent."}}`)); err != nil {
		t.Fatal(err)
	}
	if roots.Load() != 2 || children.Load() != 2 || counts.Load() != 1 {
		t.Fatal("real Agent/count cardinality", roots.Load(), children.Load(), counts.Load())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
