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
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestActualTwoWorkflowPresetsCancelOneThenRootCloseDrainsPhysicalPeers(t *testing.T) {
	type events struct {
		model, modelClosed, stream, streamClosed            chan struct{}
		modelOnce, closedOnce, streamOnce, streamClosedOnce sync.Once
		init                                                atomic.Int32
	}
	alpha := &events{model: make(chan struct{}), modelClosed: make(chan struct{}), stream: make(chan struct{}), streamClosed: make(chan struct{})}
	beta := &events{model: make(chan struct{}), modelClosed: make(chan struct{}), stream: make(chan struct{}), streamClosed: make(chan struct{})}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := alpha
		if r.URL.Path == "/beta" {
			e = beta
		}
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			e.streamOnce.Do(func() { close(e.stream) })
			<-r.Context().Done()
			e.streamClosedOnce.Do(func() { close(e.streamClosed) })
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in protocol.JSONObject
		b, _ := io.ReadAll(r.Body)
		_ = in.UnmarshalJSON(b)
		if in.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		result := json.RawMessage(`{}`)
		switch in.String("method") {
		case "initialize":
			e.init.Add(1)
			w.Header().Set("Mcp-Session-Id", r.URL.Path)
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"actual-preset-parallel","version":"1"}}`)
		case "tools/list":
			result = []byte(`{"tools":[]}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: in.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer peer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e := alpha
		if strings.Contains(string(b), "WAIT_BETA") {
			e = beta
		}
		e.modelOnce.Do(func() { close(e.model) })
		<-r.Context().Done()
		e.closedOnce.Do(func() { close(e.modelClosed) })
	}))
	defer model.Close()
	parent := backgroundAgentMainWithSetup(t, model.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
		for _, name := range []string{"alpha", "beta"} {
			dir := filepath.Join(n.CWD, ".claude", "agents")
			_ = os.MkdirAll(dir, 0700)
			markdown := fmt.Sprintf("---\nname: %s\ndescription: Actual cancel and close\ntools: Read\nmcpServers:\n  - %s:\n      type: http\n      url: %s/%s\n---\nActual %s worker", name, name, peer.URL, name, name)
			if e := os.WriteFile(filepath.Join(dir, name+".md"), []byte(markdown), 0600); e != nil {
				t.Fatal(e)
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	launched := make([]protocol.JSONObject, 2)
	if e := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for i, name := range []string{"alpha", "beta"} {
			script := fmt.Sprintf("export const meta={name:'parallel-%s',description:'Actual presets'};return await agent('WAIT_%s',{agentType:'%s'})", name, strings.ToUpper(name), name)
			var e error
			launched[i], e = parent.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(script)}), quoteMain("preset-"+name))
			if e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	wait := func(ch <-chan struct{}, name string) {
		t.Helper()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal(name, context.Cause(ctx))
		}
	}
	wait(alpha.model, "alpha actual POST")
	wait(beta.model, "beta actual POST")
	wait(alpha.stream, "alpha actual peer")
	wait(beta.stream, "beta actual peer")
	var betaTask *sessioncontroller.BackgroundTask
	if e := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, e := parent.Controller.StopBackgroundTaskOnOwner(scope, launched[0].String("taskId"), parent.options.Native.NowMillis()); e != nil {
			return e
		}
		task, e := parent.Controller.BackgroundTaskOnOwner(scope, launched[0].String("taskId"))
		if e != nil {
			return e
		}
		future, e := nativeSkillWork(parent, scope, func() (struct{}, error) { return struct{}{}, task.Join(ctx) })
		if e != nil {
			return e
		}
		if _, e = future.Await(scope); e != nil {
			return e
		}
		task, e = parent.Controller.BackgroundTaskOnOwner(scope, launched[0].String("taskId"))
		if e != nil {
			return e
		}
		if task.Data.String("status") != "killed" {
			return fmt.Errorf("actual task lost killed state %s", rawMain(task.Data))
		}
		betaTask, e = parent.Controller.BackgroundTaskOnOwner(scope, launched[1].String("taskId"))
		if e != nil {
			return e
		}
		if betaTask.Data.String("status") != "running" {
			return fmt.Errorf("cancelled another workflow %s", rawMain(betaTask.Data))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	wait(alpha.modelClosed, "alpha cancelled model")
	wait(alpha.streamClosed, "alpha inline peer cleanup")
	select {
	case <-beta.modelClosed:
		t.Fatal("cancel leaked across workflows")
	default:
	}
	select {
	case <-beta.streamClosed:
		t.Fatal("inline cleanup closed another worker")
	default:
	}
	if e := parent.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	wait(beta.modelClosed, "Root close physical model")
	wait(beta.streamClosed, "Root close physical MCP peer")
	if e := betaTask.Join(ctx); e != nil {
		t.Fatal(e)
	}
	if alpha.init.Load() != 1 || beta.init.Load() != 1 {
		t.Fatal("inline namespace initialized twice", alpha.init.Load(), beta.init.Load())
	}
}
