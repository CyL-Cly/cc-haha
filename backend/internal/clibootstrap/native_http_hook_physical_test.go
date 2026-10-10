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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualHTTPStopBlockContinuesSameHarnessPhysicalWriteReadAndShutdown(t *testing.T) {
	var target, sid string
	var posts, hooks atomic.Int32
	hookPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input protocol.JSONObject
		_ = json.NewDecoder(r.Body).Decode(&input)
		index := hooks.Add(1)
		if input.String("session_id") != sid || input.String("hook_event_name") != "Stop" || input.Get("last_assistant_message") == nil {
			t.Error("actual stop identity/input", rawMain(input))
		}
		if index == 1 {
			_, _ = io.WriteString(w, `{"decision":"block","reason":"HTTP requires physical Write and Read"}`)
		} else {
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer hookPeer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		index := posts.Add(1)
		if index > 1 && !strings.Contains(string(body), "HTTP requires physical Write and Read") {
			t.Error("Stop feedback missing from actual POST")
		}
		switch index {
		case 1:
			emitPlanFixture(w, 1, "", "BEFORE_HTTP_STOP")
		case 2:
			emitNativeTaskTool(w, "http-write", "Write", map[string]any{"file_path": target, "content": "HTTP_HOOK_PHYSICAL_RESULT"})
		case 3:
			emitNativeTaskTool(w, "http-read", "Read", map[string]any{"file_path": target})
		default:
			if !strings.Contains(string(body), "HTTP_HOOK_PHYSICAL_RESULT") {
				t.Error("physical Read missing in model round")
			}
			emitPlanFixture(w, int(index), "", "AFTER_HTTP_STOP")
		}
	}))
	defer model.Close()
	m := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		target = filepath.Join(n.CWD, "http-result.txt")
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "http", "url": hookPeer.URL}}}}}}), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Read,Write"}
		o.Arguments.PermissionMode = "bypassPermissions"
	})
	sid = m.Controller.SessionID()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual HTTP Stop test"})})); err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	if posts.Load() != 4 || hooks.Load() != 2 || m.Controller.SessionID() != sid {
		t.Fatal("same shared Harness rounds", posts.Load(), hooks.Load(), m.Controller.SessionID(), sid)
	}
	if bytes, err := os.ReadFile(target); err != nil || string(bytes) != "HTTP_HOOK_PHYSICAL_RESULT" {
		t.Fatal("physical tools", string(bytes), err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal("actual native drain", err)
	}
}

func TestActualHTTPPrePostToolHooksModifyInputPermissionAndNextModelContext(t *testing.T) {
	var original, selected, sid string
	var hooks, posts atomic.Int32
	hookPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input protocol.JSONObject
		_ = json.NewDecoder(r.Body).Decode(&input)
		hooks.Add(1)
		if input.String("session_id") != sid || input.String("tool_name") == "" || input.Get("tool_use_id") == nil {
			t.Error("real tool Hook identity", rawMain(input))
		}
		specific := map[string]any{"hookEventName": input.String("hook_event_name"), "additionalContext": "HTTP actual tool context"}
		if input.String("hook_event_name") == "PreToolUse" {
			specific["permissionDecision"] = "allow"
			if input.String("tool_name") == "Write" {
				specific["updatedInput"] = map[string]any{"file_path": selected, "content": "HTTP_UPDATED_PHYSICAL"}
			}
		} else if input.Get("tool_response") == nil {
			t.Error("Post HTTP hook did not receive actual tool response")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hookSpecificOutput": specific, "systemMessage": "HTTP_UI_ONLY"})
	}))
	defer hookPeer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		index := posts.Add(1)
		if index > 1 && !strings.Contains(string(body), "HTTP actual tool context") {
			t.Error("HTTP Hook context absent")
		}
		if strings.Contains(string(body), "HTTP_UI_ONLY") {
			t.Error("UI-only text entered model API")
		}
		switch index {
		case 1:
			emitNativeTaskTool(w, "http-tool-write", "Write", map[string]any{"file_path": original, "content": "original"})
		case 2:
			emitNativeTaskTool(w, "http-tool-read", "Read", map[string]any{"file_path": selected})
		default:
			if !strings.Contains(string(body), "HTTP_UPDATED_PHYSICAL") {
				t.Error("real modified Read feedback absent")
			}
			emitPlanFixture(w, int(index), "", "HTTP_TOOL_DONE")
		}
	}))
	defer model.Close()
	m := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		original = filepath.Join(n.CWD, "original.txt")
		selected = filepath.Join(n.CWD, "modified.txt")
		settings := map[string]any{"hooks": map[string]any{}}
		for _, event := range []string{"PreToolUse", "PostToolUse"} {
			settings["hooks"].(map[string]any)[event] = []any{map[string]any{"matcher": "Write|Read", "hooks": []any{map[string]any{"type": "http", "url": hookPeer.URL}}}}
		}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Read,Write"}
		o.Arguments.PermissionMode = "default"
	})
	sid = m.Controller.SessionID()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual HTTP tool Hooks"})})); err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	if posts.Load() != 3 || hooks.Load() != 4 {
		t.Fatal("actual Harness/Hook count", posts.Load(), hooks.Load())
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("original input executed", err)
	}
	if data, err := os.ReadFile(selected); err != nil || string(data) != "HTTP_UPDATED_PHYSICAL" {
		t.Fatal("real modified Write", string(data), err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
