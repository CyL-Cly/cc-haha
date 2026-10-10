package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualAgentPresetWorkersInheritResourcesWithoutRelistingOrClosingParent(t *testing.T) {
	var inits, lists, reads atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in protocol.JSONObject
		bytes, _ := io.ReadAll(r.Body)
		_ = in.UnmarshalJSON(bytes)
		if in.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		result := json.RawMessage(`{}`)
		switch in.String("method") {
		case "initialize":
			inits.Add(1)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{},"tools":{}},"serverInfo":{"name":"actual-inherited-resource","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[{"name":"echo","inputSchema":{"type":"object"},"future":{"keep":true}}]}`)
		case "resources/list":
			lists.Add(1)
			result = []byte(`{"resources":[{"name":"Actual resource","uri":"scheme:private","future":{"keep":true}}]}`)
		case "resources/read":
			reads.Add(1)
			result = []byte(`{"contents":[{"uri":"scheme:private","text":"PHYSICAL_RESOURCE"}]}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: in.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithSetup(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		if e := os.WriteFile(filepath.Join(n.CWD, ".mcp.json"), rawMain(map[string]any{"mcpServers": map[string]any{"parent": map[string]any{"type": "http", "url": peer.URL}}}), 0600); e != nil {
			t.Fatal(e)
		}
	})
	workers := []*MainPrint{presetChildFixture(t, parent, ""), presetChildFixture(t, parent, "")}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, child := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				resource, e := child.mcpTools.SkillResourceAttachment(ctx, scope, "parent:scheme:private")
				if e != nil {
					return e
				}
				var payload protocol.JSONObject
				_ = payload.UnmarshalJSON(resource)
				if payload.String("name") != "Actual resource" || payload.Get("content") == nil {
					t.Errorf("actual inherited resource lost %s", resource)
				}
				peers, e := child.mcpTools.AgentConnections(scope)
				if e != nil {
					return e
				}
				if len(peers) != 1 || len(peers[0].Tools) != 1 {
					t.Error("actual inherited peer missing")
					return nil
				}
				peers[0].Tools[0].Set("name", quoteMain("mutated"))
				current, e := child.mcpTools.AgentConnections(scope)
				if e != nil {
					return e
				}
				if current[0].Tools[0].String("name") != "echo" {
					t.Error("snapshot exposed mutable actual MCP catalogue")
				}
				return nil
			}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	for _, child := range workers {
		if e := child.ShutdownSession(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if e := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, e := parent.mcpTools.SkillResourceAttachment(ctx, scope, "parent:scheme:private")
		return e
	}); e != nil {
		t.Fatal("workers closed borrowed parent transport", e)
	}
	if inits.Load() != 1 || lists.Load() != 1 || reads.Load() != 3 {
		t.Fatal("worker repeated discovery or lost inherited cache", inits.Load(), lists.Load(), reads.Load())
	}
}
