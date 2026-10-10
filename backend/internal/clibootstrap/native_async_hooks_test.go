package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func TestActualMainAsyncHookWriteReadPostContextAndNextHumanSameHarness(t *testing.T) {
	var calls atomic.Int32
	var target, started, release, finished string
	var m *MainPrint
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		count := calls.Add(1)
		switch count {
		case 1:
			emitNativeTaskTool(w, "async-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_ASYNC_WRITE"})
		case 2:
			if strings.Contains(string(body), "ACTUAL_ASYNC_CONTEXT") {
				t.Error("unfinished async Hook reached the model early")
			}
			if err := os.WriteFile(release, []byte("release actual process"), 0600); err != nil {
				t.Error(err)
			}
			deadline := time.Now().Add(8 * time.Second)
			ready := false
			for !ready && time.Now().Before(deadline) {
				receipt, err := m.Owner.Submit(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
					rows, err := m.asyncHooks.Pending(scope)
					if err != nil {
						return err
					}
					if len(rows) == 1 {
						_, status, err := rows[0].Process.Snapshot()
						ready = status == "completed"
						return err
					}
					return nil
				})
				if err == nil {
					err = receipt.Wait()
				}
				if err != nil {
					t.Error(err)
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !ready {
				t.Error("actual detached Hook did not complete")
			}
			emitNativeTaskTool(w, "async-read", "Read", map[string]any{"file_path": target})
		default:
			if strings.Count(string(body), "ACTUAL_ASYNC_CONTEXT") != 1 || !strings.Contains(string(body), "ACTUAL_ASYNC_ADDITIONAL") {
				t.Error("original async attachment was not rendered once", count)
			}
			emitPlanFixture(w, 1, "", "ASYNC_DONE")
		}
	}))
	defer peer.Close()
	m = backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		target = filepath.Join(n.CWD, "async-result.txt")
		started = filepath.Join(n.CWD, "async-input.json")
		release = filepath.Join(n.CWD, "async-release")
		finished = filepath.Join(n.CWD, "async-finished")
		script := filepath.Join(n.CWD, "async-hook.py")
		code := fmt.Sprintf("import json,sys,time\nfrom pathlib import Path\nx=json.load(sys.stdin)\nPath(%q).write_text(json.dumps(x))\nwhile not Path(%q).exists():time.sleep(.005)\nprint('\\n'+json.dumps({'systemMessage':'ACTUAL_ASYNC_CONTEXT','hookSpecificOutput':{'additionalContext':'ACTUAL_ASYNC_ADDITIONAL'},'decision':'block','future':{'keep':True}}),flush=True)\nPath(%q).write_text('physical completion')\n", started, release, finished)
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		settings := map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "hooks": map[string]any{"PostToolUse": []any{map[string]any{"matcher": "Write", "hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + settingsShellQuote(script), "async": true, "timeout": .03}}}}}}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Write,Read"}
		o.HookEvents = true
		o.Arguments.IncludeHookEvents = true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err := m.RunTurn(ctx, user("actual Write then Read")); err != nil {
		t.Fatal(err)
	}
	if err := m.RunTurn(ctx, user("next human retains completed async context")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatal("extra or missing shared model requests", calls.Load())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "ACTUAL_ASYNC_WRITE" {
		t.Fatal(string(data), err)
	}
	data, err = os.ReadFile(started)
	if err != nil {
		t.Fatal(err)
	}
	var input protocol.JSONObject
	if err = input.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	if input.String("hook_event_name") != "PostToolUse" || input.String("tool_name") != "Write" || input.String("session_id") != m.Controller.SessionID() {
		t.Fatal("actual Source Hook input identity changed", input)
	}
	if _, err = os.Stat(finished); err != nil {
		t.Fatal(err)
	}
	var attachments int
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		history := m.Controller.History()
		for _, message := range history {
			if message.String("type") == "attachment" {
				var row protocol.JSONObject
				raw, err := message.MarshalJSON()
				if err != nil {
					return err
				}
				_ = row.UnmarshalJSON(raw)
				payload, _ := row.Object("attachment")
				if payload.String("type") == "async_hook_response" {
					attachments++
					response, _ := payload.Object("response")
					future, _ := response.Object("future")
					if string(future.Get("keep")) != "true" {
						return fmt.Errorf("original unknown async response fields were lost")
					}
				}
			}
		}
		return nil
	}); err != nil || attachments != 1 {
		t.Fatal("canonical async bookkeeping changed", attachments, err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestActualForegroundChildAsyncHookBorrowsParentRegistryAcrossChildClose(t *testing.T) {
	var calls atomic.Int32
	var target, release string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		count := calls.Add(1)
		switch count {
		case 1:
			emitNativeTaskTool(w, "async-child-agent", "Agent", map[string]any{"description": "actual async child", "prompt": "Write the actual child file then finish", "subagent_type": "general-purpose"})
		case 2:
			emitNativeTaskTool(w, "async-child-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_CHILD_ASYNC_WRITE"})
		case 5:
			if strings.Count(string(body), "ACTUAL_CHILD_ASYNC_CONTEXT") != 1 {
				t.Error("child completion was not injected once in actual parent request")
			}
			emitPlanFixture(w, 1, "", "PARENT_AFTER_ASYNC")
		default:
			emitPlanFixture(w, 1, "", "CHILD_ASYNC_DONE")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		target = filepath.Join(n.CWD, "child-async-result.txt")
		release = filepath.Join(n.CWD, "child-async-release")
		script := filepath.Join(n.CWD, "child-async.py")
		code := fmt.Sprintf("import json,sys,time\nfrom pathlib import Path\njson.load(sys.stdin)\nwhile not Path(%q).exists():time.sleep(.005)\nprint('\\n'+json.dumps({'systemMessage':'ACTUAL_CHILD_ASYNC_CONTEXT'}),flush=True)\n", release)
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		settings := map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "hooks": map[string]any{"SubagentStop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + settingsShellQuote(script), "async": true}}}}}}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Agent,Write,Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err := m.RunTurn(ctx, user("actual child writes and exits")); err != nil {
		t.Fatal(err)
	}
	var process queryhooks.AsyncProcess
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		rows, err := m.asyncHooks.Pending(scope)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].HookEvent != "SubagentStop" {
			return fmt.Errorf("actual child Close lost parent process registry: %+v", rows)
		}
		process = rows[0].Process
		_, status, err := process.Snapshot()
		if status != "backgrounded" {
			return fmt.Errorf("child Close killed borrowed process: %s", status)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "ACTUAL_CHILD_ASYNC_WRITE" {
		t.Fatal(string(data), err)
	}
	if err := os.WriteFile(release, []byte("real release"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.Done():
	case <-ctx.Done():
		t.Fatal("actual parent-owned child hook did not finish")
	}
	if err := m.RunTurn(ctx, user("observe completed child async hook")); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatal("extra or missing parent/child model requests", calls.Load())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestActualAsyncRewakeIdleQueuesOriginalNotificationAndNextHumanConsumesIt(t *testing.T) {
	var calls atomic.Int32
	var target, counter string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		count := calls.Add(1)
		if count == 2 {
			if !strings.Contains(string(body), `Stop hook blocking error from command \"Stop\": REAL_IDLE_REWAKE`) {
				t.Error("actual idle rewake did not reach next owning model request")
			}
			emitNativeTaskTool(w, "rewake-read", "Read", map[string]any{"file_path": target})
			return
		}
		emitPlanFixture(w, 1, "", "REWAKE_DONE")
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		target = filepath.Join(n.CWD, "rewake-input.txt")
		counter = filepath.Join(n.CWD, "rewake-hook-count")
		script := filepath.Join(n.CWD, "rewake-hook.py")
		if err := os.WriteFile(target, []byte("REAL_REWAKE_READ"), 0600); err != nil {
			t.Fatal(err)
		}
		code := fmt.Sprintf("import json,sys,time\nfrom pathlib import Path\njson.load(sys.stdin)\np=Path(%q)\nn=int(p.read_text())+1 if p.exists() else 1\np.write_text(str(n))\nif n==1:\n time.sleep(.1)\n sys.stderr.write('REAL_IDLE_REWAKE')\n sys.exit(2)\n", counter)
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		settings := map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + settingsShellQuote(script), "asyncRewake": true}}}}}}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err := m.RunTurn(ctx, user("finish then let the real idle Hook enqueue")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	queued := false
	for !queued && time.Now().Before(deadline) {
		receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			var err error
			queued, err = m.Controller.HasQueuedTaskNotificationsOnOwner(scope)
			return err
		})
		if err == nil {
			err = receipt.Wait()
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if !queued {
		t.Fatal("real asyncRewake exit2 did not wake the canonical queue at idle")
	}
	if calls.Load() != 1 {
		t.Fatal("async Hook unexpectedly started another Harness", calls.Load())
	}
	if err := m.RunTurn(ctx, user("consume actual pending notice and Read")); err != nil {
		t.Fatal(err)
	}
	// Source drains the next-priority task-notification as its own ASK before
	// accepting the next human input: Read + completion, then the human ASK.
	if calls.Load() != 4 {
		t.Fatal("actual task-notification/human FIFO did not use the same model loop", calls.Load())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestActualIdleMainShutdownCancelsAndJoinsTransferredHookProcess(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { emitPlanFixture(w, 1, "", "IDLE_BEFORE_SHUTDOWN") }))
	defer peer.Close()
	var pidFile string
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		pidFile = filepath.Join(n.CWD, "idle-hook-pid")
		script := filepath.Join(n.CWD, "idle-hook.py")
		code := fmt.Sprintf("import os,time,json,sys\nfrom pathlib import Path\njson.load(sys.stdin)\nPath(%q).write_text(str(os.getpid()))\ntime.sleep(60)\n", pidFile)
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		settings := map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + settingsShellQuote(script), "async": true}}}}}}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "finish while Hook continues"})})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	var process queryhooks.AsyncProcess
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		rows, err := m.asyncHooks.Pending(scope)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return fmt.Errorf("idle async process lost registry ownership")
		}
		process = rows[0].Process
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pid := 0
	deadline := time.Now().Add(5 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(string(raw))
		}
		time.Sleep(time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("actual transferred Hook never started")
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.Done():
	default:
		t.Fatal("shutdown returned before actual Hook joined")
	}
	_, status, err := process.Snapshot()
	if err != nil || status != "killed" {
		t.Fatal("shutdown did not cancel transferred actual process", status, err)
	}
	if exec.Command("/bin/kill", "-0", strconv.Itoa(pid)).Run() == nil {
		t.Fatal("idle shutdown leaked the actual Python Hook", pid)
	}
}
