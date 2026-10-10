package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func emitPlanFixture(w http.ResponseWriter, round int, tool, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"plan-model\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
	if tool != "" {
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"plan-%d\",\"name\":%q,\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", round, tool)
	} else {
		encoded, _ := json.Marshal(text)
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", encoded)
	}
}
func TestActualMainPrintPlanEntryApprovalEditAndRestore(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	// Source simple mode suppresses all non-queued attachments. This scenario
	// exercises the ordinary Plan attachment producer instead.
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	disabled := false
	var mu sync.Mutex
	requests := 0
	frames := []json.RawMessage{}
	modes := []string{}
	var planPath string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests++
		round := requests
		mu.Unlock()
		switch round {
		case 1:
			if !bytes.Contains(body, []byte(`"name":"EnterPlanMode"`)) || !bytes.Contains(body, []byte(`"name":"ExitPlanMode"`)) {
				t.Error("actual plan tools not advertised")
			}
			emitPlanFixture(w, round, "EnterPlanMode", "")
		case 2:
			if !bytes.Contains(body, []byte("Entered plan mode.")) || !bytes.Contains(body, []byte("## Plan File Info:")) {
				t.Error("actual entry result or shared plan attachment missing")
			}
			emitPlanFixture(w, round, "ExitPlanMode", "")
		case 3:
			if !bytes.Contains(body, []byte("Approved Plan (edited by user)")) || !bytes.Contains(body, []byte("reviewed plan")) || !bytes.Contains(body, []byte("## Exited Plan Mode")) {
				t.Error("actual approval result or shared exit attachment missing")
			}
			var request struct{ Messages []protocol.JSONObject }
			if e := json.Unmarshal(body, &request); e != nil {
				t.Error(e)
			}
			for _, message := range request.Messages {
				if message.String("role") != "assistant" {
					continue
				}
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(message.Get("content"), &blocks)
				for _, block := range blocks {
					if block.String("name") != "ExitPlanMode" {
						continue
					}
					var input protocol.JSONObject
					_ = input.UnmarshalJSON(block.Get("input"))
					if input.Get("plan") != nil || input.Get("planFilePath") != nil {
						t.Error("injected plan fields leaked into actual model POST")
					}
				}
			}
			emitPlanFixture(w, round, "", "plan completed")
		default:
			t.Errorf("unexpected model request %d", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	approvals := 0
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, OnPermissionModeChanged: func(_ context.Context, mode string) error { modes = append(modes, mode); return nil }, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual shared planning fixture", Tools: []string{"EnterPlanMode,ExitPlanMode,Read"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(frame []byte) error { frames = append(frames, append(json.RawMessage(nil), frame...)); return nil }, ResolvePermission: func(_ context.Context, _ *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
		approvals++
		if tool.Name != "ExitPlanMode" || in.String("plan") != "original plan" || in.String("planFilePath") != planPath {
			t.Fatalf("wrong approval input %s %s", tool.Name, in.Get("plan"))
		}
		edited := protocol.NewJSONObject(protocol.JSONField{Name: "plan", Value: quoteMain("reviewed plan")})
		return queryloop.ToolPermission{Behavior: "allow", Input: &edited, UpdatedInput: &edited, UserModified: true}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e := m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		planPath, e = m.Plans().Path(scope, "")
		if e != nil {
			return e
		}
		return os.WriteFile(planPath, []byte("original plan"), 0600)
	}); e != nil {
		t.Fatal(e)
	}
	var input protocol.JSONObject
	_ = input.UnmarshalJSON([]byte(`{"type":"user","uuid":"planning-user","message":{"role":"user","content":"plan this change"}}`))
	if e := m.RunTurn(context.Background(), input); e != nil {
		t.Fatal(e)
	}
	if approvals != 1 || len(modes) != 2 || modes[0] != "plan" || modes[1] != "default" {
		t.Fatal("approval or shared mode lifecycle", approvals, modes)
	}
	data, e := os.ReadFile(planPath)
	if e != nil || string(data) != "reviewed plan" {
		t.Fatal("approved plan did not reach actual file", string(data), e)
	}
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		context, e := m.Controller.PlanModeContext(scope)
		if e != nil {
			return e
		}
		if context.String("mode") != "default" || context.Get("prePlanMode") != nil {
			t.Fatal("shared permission mode not restored", context)
		}
		exited, _, e := m.state.PlanModeFlags(scope)
		if e != nil {
			return e
		}
		if !exited {
			t.Fatal("actual bootstrap exit flag absent")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	resumeOptions := m.options
	resumeOptions.Arguments.SessionID = ""
	resumeOptions.Arguments.Resume = "11111111-1111-4111-8111-111111111111"
	resumed, e := NewMainPrint(resumeOptions)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = resumed.ShutdownSession(context.Background()) })
	if e := resumed.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := resumed.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		path, e := resumed.Plans().Path(scope, "")
		if e != nil {
			return e
		}
		if path != planPath || resumed.Controller.SessionID() != "11111111-1111-4111-8111-111111111111" {
			t.Fatal("actual restart lost persisted SID/plan slug", path, planPath)
		}
		data, e := os.ReadFile(path)
		if e != nil || string(data) != "reviewed plan" {
			t.Fatal("actual restart lost physical plan", string(data), e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 3 || len(frames) == 0 {
		t.Fatal("shared model/SDK chain bypassed", requests)
	}
}

func TestActualMainPrintPlanDenialKeepsModeAndNextTurnApprovalUsesDisk(t *testing.T) {
	f, _, options := computerUseMainFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	disabled := false
	requests, approvals := 0, 0
	modes := []string{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests++
		switch requests {
		case 1, 3:
			if !bytes.Contains(body, []byte("## Plan File Info:")) {
				t.Error("active plan not projected")
			}
			emitPlanFixture(w, requests, "ExitPlanMode", "")
		case 2:
			if !bytes.Contains(body, []byte("keep planning")) || bytes.Contains(body, []byte("## Exited Plan Mode")) {
				t.Error("denial cleared actual plan mode")
			}
			emitPlanFixture(w, requests, "", "will revise")
		case 4:
			if !bytes.Contains(body, []byte("## Approved Plan:")) || !bytes.Contains(body, []byte("unchanged physical plan")) || !bytes.Contains(body, []byte("## Exited Plan Mode")) {
				t.Error("full-replacement empty approval did not use disk fallback")
			}
			emitPlanFixture(w, requests, "", "approved")
		default:
			t.Errorf("unexpected model request %d", requests)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	m, e := NewMainPrint(MainPrintOptions{ComputerUse: options, OnPermissionModeChanged: func(_ context.Context, mode string) error { modes = append(modes, mode); return nil }, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual plan denial fixture", Tools: []string{"EnterPlanMode,ExitPlanMode,Read"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 6, PermissionMode: "plan", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, ResolvePermission: func(_ context.Context, _ *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, _ queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
		approvals++
		if tool.Name != "ExitPlanMode" || in.String("plan") != "unchanged physical plan" {
			t.Fatal("incorrect real approval input", tool.Name, in)
		}
		if approvals == 1 {
			return queryloop.ToolPermission{Behavior: "deny", Input: in, Message: quoteMain("keep planning")}, nil
		}
		empty := protocol.NewJSONObject()
		return queryloop.ToolPermission{Behavior: "allow", Input: &empty, UpdatedInput: &empty}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e := m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	var path string
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		path, e = m.Plans().Path(scope, "")
		if e != nil {
			return e
		}
		return os.WriteFile(path, []byte("unchanged physical plan"), 0600)
	}); e != nil {
		t.Fatal(e)
	}
	for turn, text := range []string{"review the plan", "approve it now"} {
		var in protocol.JSONObject
		_ = in.UnmarshalJSON([]byte(fmt.Sprintf(`{"type":"user","uuid":"plan-denial-%d","message":{"role":"user","content":%q}}`, turn, text)))
		if e := m.RunTurn(context.Background(), in); e != nil {
			t.Fatal(e)
		}
		if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			p, e := m.Controller.PlanModeContext(scope)
			if e != nil {
				return e
			}
			hasExited, _, e := m.state.PlanModeFlags(scope)
			if e != nil {
				return e
			}
			if turn == 0 && (p.String("mode") != "plan" || hasExited) {
				t.Fatal("denial mutated shared plan state", p, hasExited)
			}
			if turn == 1 && (p.String("mode") != "default" || !hasExited) {
				t.Fatal("next turn approval did not restore shared mode", p, hasExited)
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	data, e := os.ReadFile(path)
	if e != nil || string(data) != "unchanged physical plan" {
		t.Fatal("no-edit approval altered native plan", string(data), e)
	}
	if requests != 4 || approvals != 2 || len(modes) != 1 || modes[0] != "default" {
		t.Fatal("actual two-turn chain differs", requests, approvals, modes)
	}
}
