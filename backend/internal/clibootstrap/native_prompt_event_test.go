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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeUserPromptAuxiliaryBlocksThenSameHostQueriesPhysicalTools(t *testing.T) {
	var target string
	auxiliary, main := atomic.Int32{}, atomic.Int32{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "You are evaluating a hook in Claude Code") {
			index := auxiliary.Add(1)
			var request protocol.JSONObject
			_ = request.UnmarshalJSON(body)
			config, _ := request.Object("output_config")
			format, _ := config.Object("format")
			if format.String("type") != "json_schema" || format.Get("schema") == nil {
				t.Error("actual Source prompt hook output schema absent", string(rawMain(config)))
			}
			if index == 2 && strings.Contains(string(body), "ORIGINAL_AUXILIARY_ERASED") {
				t.Error("generic hook prepended Source-undefined conversation history")
			}
			text := `{"ok":true}`
			if index == 1 {
				text = `{"ok":false,"reason":"actual auxiliary blocked"}`
			}
			emitPlanFixture(w, int(index), "", text)
			return
		}
		index := main.Add(1)
		if auxiliary.Load() != 2 || strings.Contains(string(body), "ORIGINAL_AUXILIARY_ERASED") {
			t.Error("main POST arrived without actual prompt decision/erase")
		}
		if index == 1 {
			emitNativeTaskTool(w, "prompt-actual-write", "Write", map[string]any{"file_path": target, "content": "actual-tools-after-prompt-auxiliary"})
		} else if index == 2 {
			emitNativeTaskTool(w, "prompt-actual-read", "Read", map[string]any{"file_path": target})
		} else {
			if !strings.Contains(string(body), "actual-tools-after-prompt-auxiliary") {
				t.Error("actual Read feedback absent")
			}
			emitPlanFixture(w, int(index+2), "", "ACTUAL_PROMPT_EVENT_DONE")
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
		target = filepath.Join(n.CWD, "prompt-auxiliary-result.txt")
		hook := map[string]any{"type": "prompt", "prompt": "Verify this actual user input: $ARGUMENTS", "model": "fixture-model", "statusMessage": "actual source prompt status"}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": []any{hook}}}}}), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Read,Write"}
		o.Arguments.PermissionMode = "bypassPermissions"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	input := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
	}
	if err := m.RunTurn(ctx, input("ORIGINAL_AUXILIARY_ERASED")); err != nil {
		t.Fatal("actual auxiliary first input", err)
	}
	defer m.ShutdownSession(context.Background())
	if auxiliary.Load() != 1 || main.Load() != 0 {
		t.Fatal("Source prompt decision bypassed actual auxiliary model", auxiliary.Load(), main.Load())
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		history := m.Controller.History()
		if len(history) != 1 || history[0].String("type") != "system" || !strings.Contains(history[0].String("content"), "actual auxiliary blocked") {
			t.Fatal("auxiliary history leaked or first input not blocked", string(rawMain(history)))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.RunTurn(ctx, input("Current actual human input")); err != nil {
		t.Fatal("actual auxiliary next input", err)
	}
	if auxiliary.Load() != 2 || main.Load() != 3 {
		t.Fatal("original auxiliary/main model ownership", auxiliary.Load(), main.Load())
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "actual-tools-after-prompt-auxiliary" {
		t.Fatal("actual Write/Read after original hook", string(data), err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for _, message := range m.Controller.History() {
			encoded, _ := json.Marshal(message)
			if strings.Contains(string(encoded), "Verify this actual user input:") {
				t.Fatal("prompt auxiliary input entered main canonical transcript")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
}
