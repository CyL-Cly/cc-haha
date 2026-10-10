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
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualWorkflowAgentIndependentMainPrintPhysicalToolsSidechainAndJournal(t *testing.T) {
	var rounds atomic.Int32
	var path string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "workflow orchestration script") {
			t.Errorf("lost Source workflow system prompt %.1000s", body)
		}
		var req struct {
			Tools    []protocol.JSONObject
			Messages []protocol.JSONObject
		}
		_ = json.Unmarshal(body, &req)
		names := []string{}
		for _, tool := range req.Tools {
			names = append(names, tool.String("name"))
		}
		if strings.Join(names, ",") != "Bash,Read,Write" {
			t.Errorf("worker must resolve actual parent permission/tool pool %v", names)
		}
		for _, message := range req.Messages {
			for _, block := range taskBlocks(message.Get("content")) {
				if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
					t.Errorf("actual workflow tool failed %s", rawMain(block))
				}
			}
		}
		switch n := rounds.Add(1); n {
		case 1:
			emitNativeTaskTool(w, "workflow-write", "Write", map[string]any{"file_path": path, "content": "actual-workflow-agent-file"})
		case 2:
			emitNativeTaskTool(w, "workflow-read", "Read", map[string]any{"file_path": path})
		default:
			if !strings.Contains(string(body), "actual-workflow-agent-file") {
				t.Error("actual child lost file/history")
			}
			emitPlanFixture(w, int(n), "", "  ACTUAL_WORKFLOW_RESULT  ")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") })
	path = filepath.Join(m.options.Native.CWD, "workflow-worker.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	program, e := workflow.CompileBody(utf16.Encode([]rune(`phase('verify');return await agent('Write then read the actual file',{label:'actual-worker'})`)))
	if e != nil {
		t.Fatal(e)
	}
	journal, e := workflow.NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"), workflow.NativeJournalFiles())
	if e != nil {
		t.Fatal(e)
	}
	events := []protocol.JSONObject{}
	agentIDs := []string{}
	options := workflow.ExecutionOptions{Program: program, RunID: "wf_worker123-abc", WorkflowName: "actual-workflow", Model: "fixture-model", Now: m.options.Native.NowMillis, GetTurnSpent: func() float64 { return 0 }, Journal: journal, Emit: func(event protocol.JSONObject) error {
		events = append(events, event.Clone())
		if id := event.String("agentId"); id != "" {
			agentIDs = append(agentIDs, id)
		}
		return nil
	}, RunAgent: m.runNativeWorkflowAgent}
	result, e := workflow.Execute(ctx, options)
	if e != nil || result.Error != "" || string(result.Result) != `"ACTUAL_WORKFLOW_RESULT"` || rounds.Load() != 3 {
		t.Fatalf("real shared Workflow worker failed %+v %v POSTs%d", result, e, rounds.Load())
	}
	data, e := os.ReadFile(path)
	if e != nil || string(data) != "actual-workflow-agent-file" {
		t.Fatal(e, string(data))
	}
	if len(agentIDs) == 0 {
		t.Fatal("real child ID/progress was never observed")
	}
	id := agentIDs[0]
	var snapshot []protocol.JSONObject
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		snapshot, e = m.Controller.BackgroundTasksOnOwner(scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if len(snapshot) != 0 {
		t.Fatalf("workflow worker incorrectly created ordinary Agent task: %s", rawMain(snapshot))
	}
	transcript := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects")
	found, metadata := false, false
	e = filepath.WalkDir(transcript, func(p string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == "agent-"+id+".jsonl" {
			data, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			found = strings.Contains(string(data), "ACTUAL_WORKFLOW_RESULT") && strings.Contains(string(data), `"isSidechain":true`)
		}
		if entry.Name() == "agent-"+id+".meta.json" {
			data, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			metadata = strings.Contains(string(data), `"querySource":"workflow_agent"`) || strings.Contains(string(data), `"workflow"`)
			if !strings.Contains(string(data), "wf_worker123-abc") || !strings.Contains(string(data), "actual-workflow") {
				t.Errorf("lost physical workflow provenance %s", data)
			}
		}
		return nil
	})
	if e != nil || !found || !metadata {
		t.Fatalf("actual flat workflow transcript missing %v transcript=%v metadata=%v root=%s", e, found, metadata, transcript)
	}
	snapshotJournal, e := journal.Load()
	if e != nil {
		t.Fatal(e)
	}
	options.JournalSnapshot = &snapshotJournal
	replay, e := workflow.Execute(ctx, options)
	if e != nil || replay.Error != "" || string(replay.Result) != string(result.Result) || rounds.Load() != 3 {
		t.Fatalf("actual native resume reran cached child %+v %v POSTs%d", replay, e, rounds.Load())
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		ledger, e := m.Controller.ModelAccountingFor(m.Owner)
		if e != nil {
			return e
		}
		v, e := ledger.Snapshot(scope)
		if e != nil {
			return e
		}
		if string(v.Get("totalInputTokens")) != "3" || string(v.Get("totalOutputTokens")) != "3" {
			t.Fatalf("workflow child accounting lost %s", rawMain(v))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
