package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSessionStartDynamicWatchRunsFileChangedAndInvalidatesBashEnvironment(t *testing.T) {
	var target, journal string
	notified := make(chan string, 10)
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1/no-model", func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		target = filepath.Join(n.CWD, "actual.env")
		journal = filepath.Join(n.CWD, "file-hook.json")
		if err := os.WriteFile(target, []byte("initial"), 0600); err != nil {
			t.Fatal(err)
		}
		body := string(rawMain(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "watchPaths": []string{target}}}))
		start := "printf '%s' '" + body + "'"
		file := `cat > "` + journal + `"; printf '%s\n' 'export ACTUAL_FILE_ENV=FILE_ENV_UPDATED_A21' >> "$CLAUDE_ENV_FILE"; printf '%s' '{"systemMessage":"ACTUAL_FILE_NOTICE_A21"}'`
		settings := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": start}}}}, "FileChanged": []any{map[string]any{"matcher": "actual.env", "hooks": []any{map[string]any{"type": "command", "command": file}}}}}}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.OnEnvironmentHookMessage = func(text string, _ bool) { notified <- text } })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var before *string
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		before, e = m.nativeSessionEnvironmentScript(ctx, scope)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if before != nil {
		t.Fatal("original null memo expected", *before)
	}
	if err := os.WriteFile(target, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-notified:
		if text != "ACTUAL_FILE_NOTICE_A21" {
			t.Fatal(text)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("actual FileChanged hook did not run")
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err = json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	if input["hook_event_name"] != "FileChanged" || input["event"] != "change" || input["file_path"] != target || input["session_id"] != m.Controller.SessionID() {
		t.Fatal(string(data))
	}
	var after *string
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		after, e = m.nativeSessionEnvironmentScript(ctx, scope)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if after == nil || !strings.Contains(*after, "FILE_ENV_UPDATED_A21") {
		t.Fatal("actual env cache was not invalidated", after)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
