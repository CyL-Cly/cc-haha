package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestActualRunningAgentMessageIsConsumedAtNextToolRound(t *testing.T) {
	started := make(chan struct{})
	resume := make(chan struct{})
	var rounds atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := rounds.Add(1)
		if n == 1 {
			close(started)
			select {
			case <-resume:
			case <-r.Context().Done():
				return
			}
			emitNativeTaskTool(w, "pending-real-bash", "Bash", map[string]any{"command": "printf TOOL_EXECUTED_PENDING_C7A2", "description": "actual pending round"})
			return
		}
		if !strings.Contains(string(body), "PENDING_COORDINATOR_C7A2") || !strings.Contains(string(body), "TOOL_EXECUTED_PENDING_C7A2") {
			t.Errorf("pending not consumed with actual tool result %.2000s", body)
		}
		emitPlanFixture(w, int(n), "", "PENDING_DELIVERED_C7A2")
	}))
	defer peer.Close()
	m := backgroundAgentMain(t, peer.URL)
	m.options.Native.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "0"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var id string
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, e := actualBackgroundAgentRequest(scope, m, "Start actual pending tool")
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
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	response, e := m.SendAgentMessage(ctx, id, "PENDING_COORDINATOR_C7A2")
	if e != nil || response.String("delivery") != "queued" {
		t.Fatal(response, e)
	}
	close(resume)
	task := waitActualAgent(t, m, id)
	if task.Data.String("status") != "completed" || string(task.Data.Get("pendingMessages")) != "[]" {
		t.Fatalf("pending real task failed %s", rawMain(task.Data))
	}
	if rounds.Load() != 2 {
		t.Fatal("pending started unrelated harness", rounds.Load())
	}
}

func waitActualAgent(t *testing.T, m *MainPrint, id string) *sessioncontroller.BackgroundTask {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var task *sessioncontroller.BackgroundTask
	for {
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			var e error
			task, e = m.nativeBackgroundBindings().Lookup(scope, id)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if task != nil && task.Data.String("status") != "running" {
			if e := task.Join(ctx); e != nil {
				t.Fatal(e)
			}
			return task
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestActualAgentContinuationUsesSameSidechainAndSpawningCardAfterColdRestore(t *testing.T) {
	var childRounds atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "private actual parent harness") {
			emitPlanFixture(w, 1, "", "ROOT_PERSISTED_C7A2")
			return
		}
		n := childRounds.Add(1)
		if n > 1 && (!strings.Contains(string(body), "ORIGINAL_AGENT_C7A2") || !strings.Contains(string(body), "FIRST_RESULT_C7A2")) {
			t.Errorf("missing original sidechain in actual request %.1800s", body)
		}
		if n == 2 && !strings.Contains(string(body), "SECOND_PROMPT_C7A2") {
			t.Error("warm resume lost prompt")
		}
		if n == 3 && (!strings.Contains(string(body), "SECOND_PROMPT_C7A2") || !strings.Contains(string(body), "THIRD_PROMPT_C7A2")) {
			t.Error("cold resume lost full original chain")
		}
		result := "FIRST_RESULT_C7A2"
		if n == 2 {
			result = "SECOND_RESULT_C7A2"
		}
		if n == 3 {
			result = "THIRD_RESULT_C7A2"
		}
		emitPlanFixture(w, int(n), "", result)
	}))
	defer peer.Close()
	m := backgroundAgentMain(t, peer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Persist actual root transcript"})})); e != nil {
		t.Fatal(e)
	}
	var id, path string
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, e := actualBackgroundAgentRequest(scope, m, "ORIGINAL_AGENT_C7A2")
		if e != nil {
			return e
		}
		request.Name = "original-worker"
		future, e := m.runBackgroundAgent(ctx, scope, request)
		if e != nil {
			return e
		}
		run, e := future.Await(scope)
		id, path = run.AgentID, run.OutputFile
		return e
	}); e != nil {
		t.Fatal(e)
	}
	first := waitActualAgent(t, m, id)
	if first.Data.String("status") != "completed" {
		t.Fatalf("first %s", rawMain(first.Data))
	}
	response, e := m.SendAgentMessage(ctx, id, " SECOND_PROMPT_C7A2 ")
	if e != nil || response.String("delivery") != "resumed" {
		t.Fatal(response, e)
	}
	second := waitActualAgent(t, m, id)
	if second.Data.String("status") != "completed" || second.Data.String("toolUseId") != "actual-bg-agent-tool" || string(second.Data.Get("startTime")) != string(first.Data.Get("startTime")) {
		t.Fatalf("same actual task identity lost %s", rawMain(second.Data))
	}
	actualPath, e := os.Readlink(path)
	if e != nil {
		t.Fatal(e)
	}
	// Historical content-replacement records may come from an enabled older
	// process. Current inactive Source profiles resume the original sidechain.
	record := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("content-replacement")}, protocol.JSONField{Name: "agentId", Value: quoteMain(id)}, protocol.JSONField{Name: "sessionId", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "replacements", Value: rawMain([]map[string]any{{"toolUseId": "old-result", "replacement": "HISTORICAL_REPLACEMENT_C7A2", "mustReapply": true}})})
	f, e := os.OpenFile(actualPath, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Write(append(rawMain(record), '\n')); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	metaPath := strings.TrimSuffix(actualPath, ".jsonl") + ".meta.json"
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: quoteMain("  deleted-original-worker  ")}, protocol.JSONField{Name: "toolUseId", Value: quoteMain("actual-bg-agent-tool")})
	if e = os.WriteFile(metaPath, rawMain(metadata), 0600); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(actualPath)
	if e != nil {
		t.Fatal(e)
	}
	sid := m.Controller.SessionID()
	options := m.options
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	options.Native = m.options.Native.agentScopedBoundary(m.options.Native.CWD)
	options.Arguments.Resume = sid
	options.Arguments.SessionID = ""
	options.OnOutput = nil
	reopened, e := NewMainPrint(options)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = reopened.ShutdownSession(context.Background()) })
	if e = reopened.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	response, e = reopened.SendAgentMessage(ctx, id, "THIRD_PROMPT_C7A2")
	if e != nil || response.String("delivery") != "resumed" {
		t.Fatal(response, e)
	}
	third := waitActualAgent(t, reopened, id)
	if third.Data.String("status") != "completed" || third.Data.String("toolUseId") != "actual-bg-agent-tool" {
		t.Fatalf("cold task %s", rawMain(third.Data))
	}
	metadataBytes, e := os.ReadFile(metaPath)
	if e != nil {
		t.Fatal(e)
	}
	var restoredMetadata protocol.JSONObject
	if e = restoredMetadata.UnmarshalJSON(metadataBytes); e != nil {
		t.Fatal(e)
	}
	if restoredMetadata.String("agentType") != "deleted-original-worker" || restoredMetadata.Get("description") != nil {
		t.Fatalf("real metadata overwrite differs from Source %s", metadataBytes)
	}
	if third.Data.String("description") != "(resumed)" {
		t.Fatalf("cold UI fallback was not retained %s", rawMain(third.Data))
	}
	after, e := os.ReadFile(actualPath)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(string(after), string(before)) {
		t.Error("resume rewrote original sidechain prefix")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(after)), "\n") {
		var row protocol.JSONObject
		if json.Unmarshal([]byte(line), &row) != nil {
			t.Fatal("invalid physical sidechain")
		}
		uuid := row.String("uuid")
		if uuid != "" {
			if seen[uuid] {
				t.Fatalf("duplicate resumed physical UUID %s", uuid)
			}
			seen[uuid] = true
		}
	}
	if childRounds.Load() != 3 {
		t.Fatal("unexpected physical inference count", childRounds.Load())
	}
}
