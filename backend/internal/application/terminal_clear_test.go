package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualPublicSessionClearAliasesFreshContextHooksAndImmutableOldTranscript(t *testing.T) {
	var mu sync.Mutex
	var requests []protocol.JSONObject
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, obj(body))
		mu.Unlock()
		response(w, "actual-clear-response", "", nil, "ACTUAL_CLEAR_ASSISTANT")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.WorkDir = o.Native.CWD
	o.Interactive = true
	o.ForegroundShellProgress = true
	o.Arguments.SystemPrompt = ""
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	hookEvents := filepath.Join(o.Environment["HOME"], "clear-hook-events.jsonl")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	hookFile := filepath.Join(o.Environment["HOME"], "clear-hook.py")
	hookScript := "import json,sys\nv=json.load(sys.stdin)\nwith open(sys.argv[1],'a') as f: f.write(json.dumps(v)+'\\n')\nif v['hook_event_name']=='SessionStart' and v.get('source')=='clear': print(json.dumps({'hookSpecificOutput':{'hookEventName':'SessionStart','additionalContext':'CLEAR_HOOK_NEW_CONTEXT'}}))\n"
	if err = os.WriteFile(hookFile, []byte(hookScript), 0600); err != nil {
		t.Fatal(err)
	}
	command := "'" + python + "' '" + hookFile + "' '" + hookEvents + "'"
	settings := map[string]any{"language": "Deutsch", "futureUser": map[string]any{"keep": true}, "hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}, "SessionEnd": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}}}
	if err = os.WriteFile(filepath.Join(config, "settings.json"), raw(settings), 0600); err != nil {
		t.Fatal(err)
	}
	memory := filepath.Join(o.WorkDir, "CLAUDE.md")
	if err = os.WriteFile(memory, []byte("CLEAR_OLD_MEMORY"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	owner, controller, host := s.process.Owner, s.process.Controller, s.process.Host
	emit := func(tui.Event) error { return nil }
	if err = s.Submit(context.Background(), tui.Input{Text: "CLEAR_FIRST_OLD_USER"}, emit); err != nil {
		t.Fatal(err)
	}
	var panel *tui.LocalPanel
	configEmit := func(e tui.Event) error {
		if e.LocalPanel != nil {
			copy := *e.LocalPanel
			panel = &copy
		}
		return nil
	}
	if handled, err := s.InvokeTerminalCommand(context.Background(), "config", "", configEmit); !handled || err != nil {
		t.Fatal(handled, err)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "edit-config", Row: "language"}, configEmit); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "save", Fields: map[string]string{"value": "日本語"}}, configEmit); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "commit-config"}, configEmit); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "CLEAR_SECOND_OLD_USER"}, emit); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	beforeSystem := string(requests[len(requests)-1].Get("system"))
	mu.Unlock()
	if !strings.Contains(beforeSystem, "Always respond in Deutsch") || strings.Contains(beforeSystem, "Always respond in 日本語") {
		t.Fatal("Config prematurely reset original section cache", beforeSystem)
	}
	if err = os.WriteFile(memory, []byte("CLEAR_NEW_MEMORY"), 0600); err != nil {
		t.Fatal(err)
	}
	if handled, err := s.InvokeTerminalCommand(context.Background(), "rename", "CLEAR_OLD_TITLE", emit); !handled || err != nil {
		t.Fatal(handled, err)
	}
	if handled, err := s.InvokeTerminalCommand(context.Background(), "color", "red", emit); !handled || err != nil {
		t.Fatal(handled, err)
	}
	for i, alias := range []string{"clear", "reset", "new"} {
		oldSID := controller.SessionID()
		oldPath := controller.Writer().Snapshot().String("path")
		oldBytes, err := os.ReadFile(oldPath)
		if err != nil {
			t.Fatal(err)
		}
		var cleared protocol.JSONObject
		clearEmit := func(e tui.Event) error {
			if e.Kind == "conversation_cleared" {
				cleared = obj(e.Result)
			}
			return nil
		}
		if err = s.Submit(context.Background(), tui.Input{Text: "/" + alias, Mode: "prompt"}, clearEmit); err != nil {
			t.Fatal("actual slash entry", alias, err)
		}
		if !cleared.IsObject() || cleared.String("sessionId") == oldSID || cleared.String("sessionId") != controller.SessionID() || cleared.String("conversationId") == "" {
			t.Fatal("Clear did not produce fresh identities", cleared)
		}
		if owner != s.process.Owner || controller != s.process.Controller || host != s.process.Host {
			t.Fatal("Clear reconstructed the canonical runtime")
		}
		if len(controller.ReadCache().Entries()) != 0 || len(obj(controller.Writer().Snapshot().Get("metadata")).OwnProperties()) != 0 {
			t.Fatal("Old live state survived", controller.Writer().Snapshot())
		}
		if !bytes.Contains(cleared.Get("history"), []byte("CLEAR_HOOK_NEW_CONTEXT")) {
			t.Fatal("Actual SessionStart(clear) message not consumed", string(cleared.Get("history")))
		}
		if err = s.Submit(context.Background(), tui.Input{Text: "CLEAR_NEW_USER_" + alias}, emit); err != nil {
			t.Fatal(err)
		}
		newPath := controller.Writer().Snapshot().String("path")
		if oldPath == newPath || !strings.HasSuffix(newPath, controller.SessionID()+".jsonl") {
			t.Fatal(oldPath, newPath)
		}
		afterOld, err := os.ReadFile(oldPath)
		if err != nil || !bytes.Equal(oldBytes, afterOld) {
			t.Fatal("Parent transcript changed after clear", err, string(afterOld))
		}
		mu.Lock()
		request := requests[len(requests)-1]
		mu.Unlock()
		wire, _ := request.MarshalJSON()
		if strings.Contains(string(wire), "CLEAR_FIRST_OLD_USER") || strings.Contains(string(wire), "CLEAR_SECOND_OLD_USER") || strings.Contains(string(wire), "CLEAR_OLD_MEMORY") || !strings.Contains(string(wire), "CLEAR_NEW_MEMORY") || !strings.Contains(string(wire), "Always respond in 日本語") {
			t.Fatal("New actual model retained old context", string(wire))
		}
		if i == 0 && !strings.Contains(string(wire), "CLEAR_HOOK_NEW_CONTEXT") {
			t.Fatal(string(wire))
		}
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventsBytes, err := os.ReadFile(hookEvents)
	if err != nil {
		t.Fatal(err)
	}
	var endClear, startClear, final int
	var prior string
	for _, line := range bytes.Split(eventsBytes, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		event := obj(line)
		if event.String("hook_event_name") == "SessionEnd" && event.String("reason") == "clear" {
			endClear++
			prior = event.String("session_id")
		}
		if event.String("hook_event_name") == "SessionStart" && event.String("source") == "clear" {
			startClear++
			if event.String("session_id") == prior {
				t.Fatal("Hook order reused previous SID", string(line))
			}
		}
		if event.String("hook_event_name") == "SessionEnd" && event.String("reason") == "other" {
			final++
		}
	}
	if endClear != 3 || startClear != 3 || final != 1 {
		t.Fatal(endClear, startClear, final, string(eventsBytes))
	}
	var saved map[string]any
	data, _ := os.ReadFile(filepath.Join(config, "settings.json"))
	if err = json.Unmarshal(data, &saved); err != nil || obj(data).String("language") != "日本語" || !bytes.Contains(data, []byte("futureUser")) {
		t.Fatal(string(data), err)
	}
}
