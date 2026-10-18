package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualCLIStreamsAgentMessageControlWhileIndependentAgentIsRunning(t *testing.T) {
	childStarted := make(chan struct{})
	allowChild := make(chan struct{})
	var childRounds, parentRounds atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "private actual parent harness") {
			n := parentRounds.Add(1)
			if n == 1 {
				emitNativeTaskTool(w, "named-original-agent-card", "Agent", map[string]any{"name": "actual-worker", "description": "Actual controllable worker", "prompt": "Wait for genuine queued followup", "run_in_background": true})
				return
			}
			emitPlanFixture(w, int(n), "", "ROOT_AGENT_CONTROL_COMPLETE_C7A2")
			return
		}
		n := childRounds.Add(1)
		if n == 1 {
			close(childStarted)
			select {
			case <-allowChild:
			case <-r.Context().Done():
				return
			}
			emitNativeTaskTool(w, "controlled-real-bash", "Bash", map[string]any{"command": "printf REAL_AGENT_CONTROL_BASH_C7A2", "description": "actual SDK pending tool"})
			return
		}
		if !strings.Contains(string(body), "REAL_STDIN_AGENT_FOLLOWUP_C7A2") {
			t.Errorf("actual child never got stdin control %.1800s", body)
		}
		emitPlanFixture(w, int(n), "", "REAL_STDIN_AGENT_DELIVERED_C7A2")
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "0"
	})
	input, write := io.Pipe()
	m.options.Input = input
	t.Cleanup(func() { _ = input.Close(); _ = write.Close() })
	var mu sync.Mutex
	events := []protocol.JSONObject{}
	started := make(chan string, 1)
	responded := make(chan struct{}, 1)
	m.options.OnOutput = func(data []byte) error {
		row := taskObject(t, data)
		mu.Lock()
		events = append(events, row)
		mu.Unlock()
		if row.String("type") == "system" && row.String("subtype") == "task_started" && row.String("task_type") == "local_agent" {
			started <- row.String("task_id")
		}
		if row.String("type") == "control_response" {
			responded <- struct{}{}
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		code, e := m.Run(ctx)
		if e == nil && code != 0 {
			e = context.Canceled
		}
		done <- e
	}()
	if _, e := io.WriteString(write, `{"type":"user","message":{"role":"user","content":"Launch named actual Agent"}}`+"\n"); e != nil {
		t.Fatal(e)
	}
	var id string
	select {
	case id = <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-childStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	control := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("control_request")}, protocol.JSONField{Name: "request_id", Value: quoteMain("actual-agent-control")}, protocol.JSONField{Name: "request", Value: rawMain(map[string]any{"subtype": "send_agent_message", "agent_id": id, "content": " REAL_STDIN_AGENT_FOLLOWUP_C7A2 "})})
	if _, e := io.WriteString(write, string(rawMain(control))+"\n"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-responded:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(allowChild)
	_ = write.Close()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, row := range events {
		if row.String("type") != "control_response" {
			continue
		}
		response, _ := row.Object("response")
		value, _ := response.Object("response")
		if response.String("subtype") == "success" && response.String("request_id") == "actual-agent-control" && value.String("agent_id") == id && value.String("delivery") == "queued" {
			found = true
		}
	}
	if !found || childRounds.Load() != 2 || parentRounds.Load() != 3 {
		t.Fatal("actual stdin/child/main cardinality", found, childRounds.Load(), parentRounds.Load())
	}
}

func TestActualRetainedResumeAppendsOnlyNewStreamAndKeepsOriginalTaskCard(t *testing.T) {
	var rounds atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := rounds.Add(1)
		if n == 1 {
			emitNativeTaskTool(w, "old-bash-count", "Bash", map[string]any{"command": "printf ORIGINAL_ONE_TOOL_C7A2", "description": "original tool"})
			return
		}
		if n == 2 {
			emitPlanFixture(w, int(n), "", "FIRST_STREAM_RESULT_C7A2")
			return
		}
		if !strings.Contains(string(body), "ORIGINAL_ONE_TOOL_C7A2") {
			t.Error("resume lost actual tool round")
		}
		emitPlanFixture(w, int(n), "", "SECOND_STREAM_RESULT_C7A2")
	}))
	defer peer.Close()
	m := backgroundAgentMain(t, peer.URL)
	var started atomic.Int32
	m.options.OnOutput = func(data []byte) error {
		row := taskObject(t, data)
		if row.String("type") == "system" && row.String("subtype") == "task_started" && row.String("task_type") == "local_agent" {
			started.Add(1)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var id string
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, e := actualBackgroundAgentRequest(scope, m, "First actual retained task")
		if e != nil {
			return e
		}
		future, e := m.runBackgroundAgent(ctx, scope, request)
		if e != nil {
			return e
		}
		run, e := future.Await(scope)
		id = run.AgentID
		return e
	}); e != nil {
		t.Fatal(e)
	}
	first := waitActualAgent(t, m, id)
	result, _ := first.Data.Object("result")
	if string(result.Get("totalToolUseCount")) != "1" {
		t.Fatal("first count", rawMain(result))
	}
	view, e := m.ViewAgentTranscript(ctx, id)
	if e != nil || string(view.Get("retain")) != "true" || string(view.Get("diskLoaded")) != "true" {
		t.Fatal(view, e)
	}
	if _, e = m.SendAgentMessage(ctx, id, "Second task from actual retained coordinator"); e != nil {
		t.Fatal(e)
	}
	second := waitActualAgent(t, m, id)
	result, _ = second.Data.Object("result")
	if string(result.Get("totalToolUseCount")) != "0" || started.Load() != 1 || second.Data.String("toolUseId") != "actual-bg-agent-tool" {
		t.Fatal("resume recounted old stream or emitted duplicate start", rawMain(second.Data), started.Load())
	}
	var messages []protocol.JSONObject
	_ = json.Unmarshal(second.Data.Get("messages"), &messages)
	found := false
	for _, row := range messages {
		api, _ := row.Object("message")
		if strings.Contains(string(api.Get("content")), "SECOND_STREAM_RESULT_C7A2") {
			found = true
		}
	}
	if !found {
		t.Fatal("retained real stream was not appended", rawMain(second.Data))
	}
	if e = m.ExitAgentTranscript(ctx); e != nil {
		t.Fatal(e)
	}
}
