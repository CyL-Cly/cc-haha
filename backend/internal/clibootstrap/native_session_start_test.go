package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualSessionStartHistoryEnvironmentAndSDKInitialInput(t *testing.T) {
	var calls atomic.Int32
	var cwd string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		if !strings.Contains(string(body), "REAL_SESSION_START_CONTEXT_A21") {
			t.Error("startup context missing from actual POST")
		}
		if n == 1 {
			emitNativeTaskTool(w, "startup-bash", "Bash", map[string]any{"command": "printf '%s' \"$ACTUAL_STARTUP_ENV\""})
			return
		}
		if !strings.Contains(string(body), "REAL_SESSION_START_ENV_A21") {
			t.Error("startup env not executed by actual Bash")
		}
		emitPlanFixture(w, 2, "", "STARTUP_COMPLETED")
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		cwd = n.CWD
		hook := `printf '%s\n' 'export ACTUAL_STARTUP_ENV=REAL_SESSION_START_ENV_A21' >> "$CLAUDE_ENV_FILE"; printf '%s' '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"REAL_SESSION_START_CONTEXT_A21","initialUserMessage":"REAL_SESSION_START_INITIAL_A21"}}'`
		settings := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"matcher": "startup", "hooks": []any{map[string]any{"type": "command", "command": hook}}}}}}
		data, _ := json.Marshal(settings)
		if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), data, 0600); e != nil {
			t.Fatal(e)
		}
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var initial json.RawMessage
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		initial, e = m.hook.TakeInitialUserMessage(scope)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if string(initial) != `"REAL_SESSION_START_INITIAL_A21"` {
		t.Fatal("actual initial prompt", string(initial))
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run actual startup env Bash"})})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
	rows := m.Controller.History()
	found := false
	for _, row := range rows {
		a, _ := row.Object("attachment")
		found = found || a.String("type") == "hook_additional_context"
	}
	if !found {
		t.Fatal("actual retained startup attachment absent")
	}
	if _, err := os.Stat(filepath.Join(cwd, "must-not-be-created")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
