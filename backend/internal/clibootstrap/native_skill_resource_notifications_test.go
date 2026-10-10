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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeSkillResourceListChangedRefreshesSamePeerAndDrainsOnShutdown(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	var lists, initializations atomic.Int32
	started, closed := make(chan struct{}), make(chan struct{})
	notification := make(chan struct{}, 2)
	releaseRefresh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRefresh) }) }
	defer release()
	listed := make(chan struct{}, 4)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			close(started)
			defer close(closed)
			for {
				select {
				case <-notification:
					fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/resources/list_changed\",\"params\":{}}\n\n")
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(data)
		if input.Get("id") == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result json.RawMessage
		switch input.String("method") {
		case "initialize":
			initializations.Add(1)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{"listChanged":true}},"serverInfo":{"name":"owned-resource-notifications","version":"1"}}`)
		case "resources/list":
			index := lists.Add(1)
			name := "before"
			if index > 1 {
				name = "after"
			}
			result = []byte(`{"resources":[{"uri":"scheme:` + name + `","name":"` + name + `"}]}`)
			listed <- struct{}{}
			if index == 2 {
				select {
				case <-releaseRefresh:
				case <-r.Context().Done():
					return
				}
			}
		case "resources/read":
			params, _ := input.Object("params")
			result = rawMain(map[string]any{"contents": []map[string]string{{"uri": params.String("uri"), "text": "physical notification refresh"}}})
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	t.Cleanup(peer.Close)
	global, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": map[string]string{"type": "http", "url": peer.URL}}})
	f.write(t, filepath.Join(f.config, ".claude.json"), string(global))
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "no model resource notification test", Tools: []string{"Skill,Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual peer SSE stream did not start")
	}
	<-listed
	notification <- struct{}{}
	select {
	case <-listed:
	case <-ctx.Done():
		t.Fatal("physical list_changed did not invoke actual peer resources/list")
	}
	// Keep the first refresh unsettled while a second physical notification
	// arrives. Source keeps its subscription for the whole client lifetime.
	notification <- struct{}{}
	select {
	case <-listed:
	case <-ctx.Done():
		t.Fatal("second physical list_changed was dropped during refresh")
	}
	release()
	// The server saw the new request; join the shared Owner to verify that the
	// actual refreshed catalogue, rather than a fixed response, is consumed.
	for {
		var ready bool
		receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			value, e := m.mcpTools.SkillResourceAttachment(ctx, scope, "fixture:scheme:after")
			if e != nil {
				return e
			}
			ready = len(value) > 0
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			t.Fatal(e)
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("new resource was not installed in shared catalogue")
		case <-time.After(time.Millisecond):
		}
	}
	if lists.Load() != 3 || initializations.Load() != 1 {
		t.Fatal("notifications opened another peer or lost an in-flight refresh", lists.Load(), initializations.Load())
	}
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("actual notification stream survived Harness shutdown")
	}
}
