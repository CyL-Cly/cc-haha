package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func backgroundAgentMain(t *testing.T, url string) *MainPrint {
	return backgroundAgentMainWithSetup(t, url, nil)
}
func backgroundAgentMainWithSetup(t *testing.T, url string, setup func(*NativeBoundary)) *MainPrint {
	return backgroundAgentMainWithOptions(t, url, setup, nil)
}
func backgroundAgentMainWithOptions(t *testing.T, url string, setup func(*NativeBoundary), configure func(*MainPrintOptions)) *MainPrint {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_BASE_URL"] = url
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-bg-agent-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	if setup != nil {
		setup(f.n)
	}
	disabled := false
	options := MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private actual parent harness", Tools: []string{"Agent,Bash,TaskOutput,TaskStop,Write,Read"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard}
	if configure != nil {
		configure(&options)
	}
	m, e := NewMainPrint(options)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := m.ShutdownSession(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return m
}

func actualBackgroundAgentRequest(scope *cooperative.Scope, m *MainPrint, prompt string) (builtintools.AgentRequest, error) {
	definitions, e := m.agents.List(scope)
	if e != nil {
		return builtintools.AgentRequest{}, e
	}
	for _, def := range definitions.Active {
		if def.Name == "general-purpose" {
			permission, e := m.nativeToolPermissionContext(scope)
			if e != nil {
				return builtintools.AgentRequest{}, e
			}
			return builtintools.AgentRequest{AgentType: def.Name, DefinitionModel: def.Model, Source: def.Source, SelectedAgent: def.Fields.Clone(), Prompt: prompt, Description: "Actual isolated background Agent", ParentTools: []string{"Read", "Write", "Bash", "TaskOutput", "TaskStop"}, Tools: []string{"Read", "Write", "Bash", "TaskOutput", "TaskStop"}, PermissionContext: permission, ParentModel: "fixture-model", ParentPermissionMode: permission.String("mode"), SystemPromptSections: []json.RawMessage{quoteMain(def.SystemPrompt), quoteMain("Actual fixture child instruction")}, ToolUseID: quoteMain("actual-bg-agent-tool")}, nil
		}
	}
	return builtintools.AgentRequest{}, mainCap("actual general purpose fixture definition")
}

func TestActualDetachedBackgroundAgentSurvivesParentTurnCancellationAndWritesSidechain(t *testing.T) {
	var rounds atomic.Int32
	var outputPath string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct{ Messages []protocol.JSONObject }
		_ = json.Unmarshal(body, &request)
		for _, message := range request.Messages {
			for _, block := range taskBlocks(message.Get("content")) {
				if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
					t.Errorf("actual child tool failed %s", rawMain(block))
				}
			}
		}
		round := rounds.Add(1)
		switch round {
		case 1:
			emitNativeTaskTool(w, "child-write", "Write", map[string]any{"file_path": outputPath, "content": "actual-independent-agent"})
		case 2:
			if !strings.Contains(string(body), "child-write") {
				t.Error("child lost actual tool history")
			}
			emitNativeTaskTool(w, "child-read", "Read", map[string]any{"file_path": outputPath})
		default:
			if !strings.Contains(string(body), "actual-independent-agent") {
				t.Error("child did not read physical artifact")
			}
			emitPlanFixture(w, int(round), "", "ACTUAL_BG_AGENT_FINISHED")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMain(t, peer.URL)
	outputPath = filepath.Join(m.options.Native.CWD, "background-produced.txt")
	parentTurn, cancelParent := context.WithCancel(context.Background())
	var launched builtintools.AgentRunResult
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, e := actualBackgroundAgentRequest(scope, m, "Write then read the physical artifact")
		if e != nil {
			return e
		}
		future, e := m.runBackgroundAgent(parentTurn, scope, request)
		if e != nil {
			return e
		}
		launched, e = future.Await(scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	cancelParent()
	if !launched.Async || launched.AgentID == "" || !strings.Contains(launched.OutputFile, m.Controller.SessionID()) {
		t.Fatal(launched)
	}
	var terminal protocol.JSONObject
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for {
			entry, e := m.Controller.BackgroundTaskOnOwner(scope, launched.AgentID)
			if e != nil {
				return e
			}
			if entry == nil {
				return mainCap("actual background task disappeared")
			}
			terminal = entry.Data
			if terminal.String("status") != "running" {
				if entry.Join != nil {
					join, e := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, entry.Join(ctx) })
					if e != nil {
						return e
					}
					_, e = join.Await(scope)
					return e
				}
				return nil
			}
			wake := make(chan struct{})
			timer := time.AfterFunc(20*time.Millisecond, func() { close(wake) })
			e = scope.AwaitPromise(wake)
			timer.Stop()
			if e != nil {
				return e
			}
		}
	}); e != nil {
		t.Fatal(e)
	}
	if terminal.String("status") != "completed" || rounds.Load() != 3 {
		t.Fatalf("parent cancellation killed actual independent child %s rounds%d", rawMain(terminal), rounds.Load())
	}
	progress, ok := terminal.Object("progress")
	last, _ := progress.Object("lastActivity")
	if !ok || string(progress.Get("toolUseCount")) != "2" || last.String("toolName") != "Read" || string(last.Get("isRead")) != "true" {
		t.Fatalf("actual child did not update the one parent task progress: %s", rawMain(terminal))
	}
	data, e := os.ReadFile(outputPath)
	if e != nil || string(data) != "actual-independent-agent" {
		t.Fatal(string(data), e)
	}
	transcript, e := os.ReadFile(launched.OutputFile)
	if e != nil || !strings.Contains(string(transcript), "ACTUAL_BG_AGENT_FINISHED") || !strings.Contains(string(transcript), `"isSidechain":true`) {
		t.Fatalf("not a physical sidechain %v %.1600s", e, transcript)
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		accounting, e := m.Controller.ModelAccountingFor(m.Owner)
		if e != nil {
			return e
		}
		usage, e := accounting.Snapshot(scope)
		if e != nil {
			return e
		}
		if string(usage.Get("totalInputTokens")) != "3" || string(usage.Get("totalOutputTokens")) != "3" {
			t.Fatal("actual child usage did not reach parent ledger", usage)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func taskBlocks(raw json.RawMessage) []protocol.JSONObject {
	var blocks []protocol.JSONObject
	_ = json.Unmarshal(raw, &blocks)
	return blocks
}

func TestActualDetachedBackgroundAgentStopAndParentShutdownJoinNativeChild(t *testing.T) {
	for _, action := range []string{"stop", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			closed := make(chan struct{}, 1)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"blocked-actual-child\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				closed <- struct{}{}
			}))
			defer peer.Close()
			m := backgroundAgentMain(t, peer.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var launched builtintools.AgentRunResult
			if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				request, e := actualBackgroundAgentRequest(scope, m, "Wait for actual model stream")
				if e != nil {
					return e
				}
				future, e := m.runBackgroundAgent(ctx, scope, request)
				if e != nil {
					return e
				}
				launched, e = future.Await(scope)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("actual child model request did not enter")
			}
			if action == "stop" {
				taskToolCall(t, m, "TaskStop", `{"task_id":"`+launched.AgentID+`"}`)
			} else {
				if e := m.ShutdownSession(ctx); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("actual child stream remained alive")
			}
			if action == "stop" {
				if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					entry, e := m.Controller.BackgroundTaskOnOwner(scope, launched.AgentID)
					if e != nil {
						return e
					}
					if entry == nil || entry.Data.String("status") != "killed" {
						t.Fatal("stop did not affect sole actual task registry")
					}
					future, e := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, entry.Join(ctx) })
					if e != nil {
						return e
					}
					_, e = future.Await(scope)
					return e
				}); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
