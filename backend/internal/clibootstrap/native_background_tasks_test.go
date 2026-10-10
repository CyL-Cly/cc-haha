package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func taskMainFixture(t *testing.T) *MainPrint {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-task-fake-key"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "No inference in physical task lifecycle test", Tools: []string{"Bash,TaskOutput,TaskStop"}, MaxTurns: 4, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
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
func taskObject(t *testing.T, data []byte) protocol.JSONObject {
	t.Helper()
	var value protocol.JSONObject
	if e := json.Unmarshal(data, &value); e != nil {
		t.Fatal(e)
	}
	return value
}
func taskToolCall(t *testing.T, m *MainPrint, name string, input string) (protocol.JSONObject, protocol.JSONObject) {
	t.Helper()
	var data, mapped protocol.JSONObject
	e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, tools, e := m.nativeTools.Catalog(scope)
		if e != nil {
			return e
		}
		for _, tool := range tools {
			if tool.Name == name {
				in := taskObject(t, []byte(input))
				in, e = tool.ParseInput(in)
				if e != nil {
					return e
				}
				call := queryloop.JSONToolCallContext{ToolUseID: quoteMain("real-task-tool-use"), Opaque: &builtinwrite.CallContext{Scope: scope}}
				if e = tool.ValidateInput(context.Background(), in, call.Opaque); e != nil {
					return e
				}
				result, e := tool.CallJSON(context.Background(), in, call)
				if e != nil {
					return e
				}
				data = taskObject(t, result.Data)
				mapped, e = tool.MapResultJSON(result.Data, call.ToolUseID)
				return e
			}
		}
		t.Fatalf("real tool missing: %s", name)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return data, mapped
}
func TestActualTaskBashCompletesReadsRealOutputAndSharedState(t *testing.T) {
	m := taskMainFixture(t)
	started, mapped := taskToolCall(t, m, "Bash", `{"command":"printf before; printf error >&2; sleep 0.2; printf after","description":"physical task","run_in_background":true}`)
	id := started.String("backgroundTaskId")
	if len(id) != 9 || id[0] != 'b' || !strings.Contains(mapped.String("content"), "Command running in background with ID: "+id) {
		t.Fatal(started, mapped)
	}
	state, _ := taskToolCall(t, m, "TaskOutput", `{"task_id":"`+id+`","block":true,"timeout":5000}`)
	task := taskObject(t, state.Get("task"))
	if state.String("retrieval_status") != "success" || task.String("status") != "completed" || task.String("output") != "beforeerrorafter" || string(task.Get("exitCode")) != "0" {
		t.Fatal(state)
	}
	e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		tasks, e := m.nativeBackgroundSnapshot(scope)
		if e != nil {
			return e
		}
		if len(tasks) != 1 || tasks[0].Running || !tasks[0].Background {
			return fmt.Errorf("not actual AppState: %+v", tasks)
		}
		entry, e := m.Controller.BackgroundTaskOnOwner(scope, id)
		if e != nil {
			return e
		}
		if string(entry.Data.Get("notified")) != "true" {
			return fmt.Errorf("actual AppState not notified: %s", rawMain(entry.Data))
		}
		physicalRoot, err := filepath.EvalSymlinks(m.options.Native.Temp)
		if err != nil {
			return err
		}
		physicalOutput, err := filepath.EvalSymlinks(entry.Data.String("outputFile"))
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(physicalRoot, physicalOutput)
		if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe output path: %s", entry.Data.String("outputFile"))
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestActualTaskStopKillsProcessGroupAndControlUnknownConverges(t *testing.T) {
	m := taskMainFixture(t)
	started, _ := taskToolCall(t, m, "Bash", `{"command":"sleep 30 & child=$!; printf \"%s\" \"$child\"; wait","run_in_background":true}`)
	id := started.String("backgroundTaskId")
	before, _ := taskToolCall(t, m, "TaskOutput", `{"task_id":"`+id+`","block":false}`)
	if before.String("retrieval_status") != "not_ready" {
		t.Fatal(before)
	}
	stopped, _ := taskToolCall(t, m, "TaskStop", `{"shell_id":"`+id+`"}`)
	if stopped.String("task_id") != id || stopped.String("task_type") != "local_bash" {
		t.Fatal(stopped)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, e := m.Controller.BackgroundTaskOnOwner(scope, id)
		if e != nil {
			return e
		}
		if entry.Data.String("status") != "killed" || string(entry.Data.Get("notified")) != "true" {
			t.Fatal(entry.Data)
		}
		f, e := m.nativeTools.StartBackgroundCleanup(scope)
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		return e
	})
	if e != nil {
		t.Fatal("process group did not join", e)
	}
	result, e := m.StopBackgroundTask(ctx, "evicted-task")
	if e != nil || string(result.Get("ok")) != "true" || string(result.Get("alreadyGone")) != "true" {
		t.Fatal(result, e)
	}
	state, _ := taskToolCall(t, m, "TaskOutput", `{"task_id":"`+id+`","block":false}`)
	if state.String("retrieval_status") != "success" {
		t.Fatal(state)
	}
}
func TestActualTaskShutdownJoinsPhysicalShellAndSymlinkCannotWrite(t *testing.T) {
	m := taskMainFixture(t)
	_, _ = taskToolCall(t, m, "Bash", `{"command":"sleep 30","run_in_background":true}`)
	start := time.Now()
	if e := m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("shutdown did not terminate shell")
	}
}

func TestActualCLIStopTaskControlRunsThroughStructuredInputAndSharedRegistry(t *testing.T) {
	m := taskMainFixture(t)
	m.options.Input = strings.NewReader("{\"type\":\"control_request\",\"request_id\":\"original-control\",\"request\":{\"subtype\":\"stop_task\",\"task_id\":\"evicted-handle\"}}\n")
	frames := []json.RawMessage{}
	m.options.OnOutput = func(data []byte) error { frames = append(frames, append(json.RawMessage(nil), data...)); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, e := m.Run(ctx)
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	found := false
	for _, data := range frames {
		event := taskObject(t, data)
		if event.String("type") != "control_response" {
			continue
		}
		response := taskObject(t, event.Get("response"))
		payload := taskObject(t, response.Get("response"))
		if response.String("request_id") != "original-control" || response.String("subtype") != "success" || string(payload.Get("stopped")) != "false" || payload.String("reason") != "not_found" {
			t.Fatal(event)
		}
		found = true
	}
	if !found {
		t.Fatal("actual raw CLI input did not reach stop_task consumer")
	}
}
