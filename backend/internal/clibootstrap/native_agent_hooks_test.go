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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestActualAgentHookSharedQueryPermissionsPhysicalTranscriptAndStructuredRetry(t *testing.T) {
	var mainCalls, hookCalls atomic.Int32
	var target, transcript string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "You are verifying a stop condition") {
			mainCalls.Add(1)
			emitPlanFixture(w, 1, "", "MAIN_CANONICAL_AGENT_HOOK_732")
			return
		}
		n := hookCalls.Add(1)
		var req protocol.JSONObject
		_ = req.UnmarshalJSON(body)
		var tools []protocol.JSONObject
		_ = json.Unmarshal(req.Get("tools"), &tools)
		seen := map[string]bool{}
		for _, tool := range tools {
			seen[tool.String("name")] = true
		}
		for _, name := range []string{"Agent", "TaskOutput", "TaskStop", "EnterPlanMode", "ExitPlanMode", "Workflow", "AskUserQuestion"} {
			if seen[name] {
				t.Error("Source-disallowed hook tool advertised", name)
			}
		}
		if !seen["StructuredOutput"] || !seen["Read"] || !seen["Write"] {
			t.Error("actual hook catalog incomplete", seen)
		}
		if n == 1 && strings.Contains(string(body), "MAIN_CANONICAL_AGENT_HOOK_732") {
			t.Error("parent conversation copied into hook query")
		}
		switch n {
		case 1:
			emitNativeTaskTool(w, "hook-denied-write", "Write", map[string]any{"file_path": target, "content": "must-not-write"})
		case 2:
			if !strings.Contains(string(body), messages.DontAskRejectMessage("Write")) {
				t.Error("actual dontAsk denial missing from next model POST")
			}
			emitNativeTaskTool(w, "hook-transcript-read", "Read", map[string]any{"file_path": transcript})
		case 3:
			if !strings.Contains(string(body), "MAIN_CANONICAL_AGENT_HOOK_732") {
				t.Error("physical transcript contents absent from hook Read feedback")
			}
			emitNativeTaskTool(w, "hook-invalid-structured", "StructuredOutput", map[string]any{"ok": "invalid"})
		case 4:
			if !strings.Contains(string(body), "The parameter `ok` type is expected as `boolean` but provided as `string`") {
				var msgs []protocol.JSONObject
				_ = json.Unmarshal(req.Get("messages"), &msgs)
				blocks := []protocol.JSONObject{}
				for _, msg := range msgs {
					for _, block := range taskBlocks(msg.Get("content")) {
						if block.String("tool_use_id") == "hook-invalid-structured" {
							blocks = append(blocks, block)
						}
					}
				}
				t.Error("original schema failure not sent to actual next POST", string(rawMain(blocks)))
			}
			emitNativeTaskTool(w, "hook-actual-structured", "StructuredOutput", map[string]any{"ok": true, "future": "must-strip"})
		default:
			t.Error("unexpected actual hook model request", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		target = filepath.Join(n.CWD, "hook-must-not-write.txt")
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Create actual parent transcript"})})
	if e := m.RunTurn(ctx, user); e != nil {
		t.Fatal(e)
	}
	var before []byte
	var permissions protocol.JSONObject
	var permissionSnapshot []byte
	var result protocol.JSONObject
	e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		transcript, e = m.Controller.Writer().ComputedTranscriptPath(scope)
		if e != nil {
			return e
		}
		before = rawMain(m.Controller.History())
		permissions, e = m.Host.NativePermissionContext(scope)
		if e != nil {
			return e
		}
		permissionSnapshot = rawMain(permissions)
		initial := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain("ACTUAL_AGENT_HOOK_ISOLATED_PROMPT_732")}))
		hook := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("agent")}, protocol.JSONField{Name: "prompt", Value: quoteMain("ACTUAL_AGENT_HOOK_ISOLATED_PROMPT_732")})
		call := &builtinwrite.CallContext{Scope: scope, MainLoopModel: "fixture-model", PermissionContext: m.Host.NativePermissionContext}
		e = m.Host.QueryAgentHook(ctx, scope, queryhooks.AgentModelInput{Hook: hook, Prompt: initial, HookAgentID: "hook-agent-physical-732", Model: "fixture-model", SystemPrompt: "You are verifying a stop condition in Claude Code. The conversation transcript is available at: " + transcript, TranscriptPath: transcript, ToolUseContext: call}, func(message queryloop.Message) (bool, error) {
			if message.Type() == "attachment" {
				a, _ := message.Record.Object("attachment")
				if a.String("type") == "structured_output" {
					result, _ = a.Object("data")
					return false, nil
				}
			}
			return true, nil
		})
		if e != nil {
			return e
		}
		if !m.hook.HasAgentStructuredOutput("hook-agent-physical-732", "Stop") || m.hook.HasAgentStructuredOutput("hook-agent-physical-732", "SubagentStop") {
			return fmt.Errorf("original exact-event transient registration lost")
		}
		m.hook.ClearNativeAgentHooks("hook-agent-physical-732")
		if m.hook.HasAgentStructuredOutput("hook-agent-physical-732", "Stop") {
			return fmt.Errorf("temporary hook not removed")
		}
		after, e := m.Host.NativePermissionContext(scope)
		if e != nil {
			return e
		}
		if string(rawMain(after)) != string(permissionSnapshot) || string(rawMain(permissions)) != string(permissionSnapshot) || string(rawMain(m.Controller.History())) != string(before) {
			return fmt.Errorf("temporary agent context mutated parent permissions/history")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if hookCalls.Load() != 4 || mainCalls.Load() != 1 || string(result.Get("ok")) != "true" || result.Get("future") != nil {
		t.Fatal("actual hook chain", hookCalls.Load(), mainCalls.Load(), string(rawMain(result)))
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("dontAsk actually wrote denied file", e)
	}
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestActualConfiguredAgentStopContinuesMainToolsAndCleansSameOwner(t *testing.T) {
	var mainCalls, hookCalls atomic.Int32
	var target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "You are verifying a stop condition") {
			n := hookCalls.Add(1)
			switch n {
			case 1:
				var req protocol.JSONObject
				_ = req.UnmarshalJSON(body)
				var system []protocol.JSONObject
				_ = json.Unmarshal(req.Get("system"), &system)
				text := ""
				for _, block := range system {
					text += block.String("text")
				}
				prefix := "The conversation transcript is available at: "
				start := strings.Index(text, prefix)
				if start < 0 {
					t.Error("actual transcript prompt missing")
					http.Error(w, "missing transcript", 500)
					return
				}
				path := strings.SplitN(text[start+len(prefix):], "\n", 2)[0]
				emitNativeTaskTool(w, "agent-stop-transcript", "Read", map[string]any{"file_path": path})
			case 2:
				if !strings.Contains(string(body), "ACTUAL_PARENT_FIRST_REPLY_947") {
					t.Error("real transcript read failed before Stop decision")
				}
				emitNativeTaskTool(w, "agent-stop-block", "StructuredOutput", map[string]any{"ok": false, "reason": "Write and read the actual final marker"})
			case 3:
				emitNativeTaskTool(w, "agent-stop-result-read", "Read", map[string]any{"file_path": target})
			case 4:
				// Source spreads the parent fileReadState into the hook context.
				// Its fresh query therefore retains the parent's unchanged stub.
				if !strings.Contains(string(body), "File unchanged since last read. The content from the earlier Read tool_result in this conversation is still current") {
					var req protocol.JSONObject
					_ = req.UnmarshalJSON(body)
					var msgs []protocol.JSONObject
					_ = json.Unmarshal(req.Get("messages"), &msgs)
					blocks := []protocol.JSONObject{}
					for _, msg := range msgs {
						for _, block := range taskBlocks(msg.Get("content")) {
							if block.String("tool_use_id") == "agent-stop-result-read" {
								blocks = append(blocks, block)
							}
						}
					}
					t.Error("original shared Read cache was replaced for Hook", string(rawMain(blocks)))
				}
				emitNativeTaskTool(w, "agent-stop-success", "StructuredOutput", map[string]any{"ok": true})
			default:
				t.Error("unexpected hook request", n)
				http.Error(w, "unexpected", 500)
			}
			return
		}
		n := mainCalls.Add(1)
		switch n {
		case 1:
			emitPlanFixture(w, 1, "", "ACTUAL_PARENT_FIRST_REPLY_947")
		case 2:
			if !strings.Contains(string(body), "Stop hook feedback:\\nAgent hook condition was not met: Write and read the actual final marker") {
				t.Error("original Agent blocking feedback failed to resume main Harness")
			}
			emitNativeTaskTool(w, "parent-after-agent-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_AGENT_STOP_FINAL_FILE_947"})
		case 3:
			emitNativeTaskTool(w, "parent-after-agent-read", "Read", map[string]any{"file_path": target})
		case 4:
			emitPlanFixture(w, 4, "", "ACTUAL_PARENT_FINAL_REPLY_947")
		default:
			t.Error("unexpected parent request", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		target = filepath.Join(n.CWD, "actual-agent-stop.txt")
		data := rawMain(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "agent", "prompt": "Verify current completion: $ARGUMENTS", "model": "fixture-model", "statusMessage": "Verify actual Agent Stop", "timeout": 10}}}}}})
		if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), data, 0600); e != nil {
			t.Fatal(e)
		}
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Finish current work with actual Stop verification"})})
	var beforePermission []byte
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		p, e := m.Host.NativePermissionContext(scope)
		beforePermission = rawMain(p)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.RunTurn(ctx, user); e != nil {
		t.Fatal(e)
	}
	if mainCalls.Load() != 4 || hookCalls.Load() != 4 {
		t.Fatal("real Stop loop", mainCalls.Load(), hookCalls.Load())
	}
	if data, e := os.ReadFile(target); e != nil || string(data) != "ACTUAL_AGENT_STOP_FINAL_FILE_947" {
		t.Fatal("actual Write result", string(data), e)
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		p, e := m.Host.NativePermissionContext(scope)
		if e != nil {
			return e
		}
		if string(rawMain(p)) != string(beforePermission) {
			return fmt.Errorf("Agent Hook changed persistent main permission")
		}
		for _, record := range m.Controller.History() {
			message, _ := record.Object("message")
			if strings.Contains(string(message.Get("content")), "Verify current completion:") {
				return fmt.Errorf("Agent Hook prompt leaked into main history")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
}
