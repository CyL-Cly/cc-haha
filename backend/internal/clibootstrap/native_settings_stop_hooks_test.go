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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSettingsStopCommandContinuesSameHostThroughWriteAndRead(t *testing.T) {
	var calls atomic.Int32
	var resultPath, journal string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			emitPlanFixture(w, 1, "", "INITIAL_STOP")
		case 2:
			if !strings.Contains(string(body), "SETTINGS_STOP_RETRY") {
				t.Error("actual Stop feedback missing from same Host POST")
			}
			emitNativeTaskTool(w, "stop-write", "Write", map[string]any{"file_path": resultPath, "content": "actual-settings-stop-file"})
		case 3:
			if !strings.Contains(string(body), "stop-write") {
				t.Error("actual Write result missing")
			}
			emitNativeTaskTool(w, "stop-read", "Read", map[string]any{"file_path": resultPath})
		case 4:
			if !strings.Contains(string(body), "actual-settings-stop-file") {
				t.Error("actual Read bytes missing from model POST")
			}
			emitPlanFixture(w, 4, "", "STOP_COMPLETED")
		default:
			t.Error("unexpected model POST")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		resultPath = filepath.Join(n.CWD, "actual-stop-result.txt")
		journal = filepath.Join(n.CWD, "actual-settings-hook.jsonl")
		script := filepath.Join(n.CWD, "settings-stop.py")
		code := fmt.Sprintf("import sys,json,os\nx=json.load(sys.stdin)\np=%q\nold=os.path.exists(p)\nwith open(p,'a') as f:f.write(json.dumps(x)+'\\n')\nprint(json.dumps({} if old else {'decision':'block','reason':'SETTINGS_STOP_RETRY'}))\n", journal)
		if e := os.WriteFile(script, []byte(code), 0600); e != nil {
			t.Fatal(e)
		}
		settings := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"matcher": "ignored-for-Stop", "hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + strconv.Quote(script), "statusMessage": "actual settings hook"}}}}}}
		raw, _ := json.Marshal(settings)
		if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Complete actual settings Hook task"})})
	if e := m.RunTurn(ctx, user); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 4 {
		t.Fatal("same Host model count", calls.Load())
	}
	b, e := os.ReadFile(resultPath)
	if e != nil || string(b) != "actual-settings-stop-file" {
		t.Fatal("physical Write/Read", e, string(b))
	}
	b, e = os.ReadFile(journal)
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatal("actual Hook invocation count", string(b))
	}
	for i, line := range lines {
		var input protocol.JSONObject
		_ = input.UnmarshalJSON([]byte(line))
		if input.String("session_id") != m.Controller.SessionID() || input.String("hook_event_name") != "Stop" || string(input.Get("stop_hook_active")) != []string{"false", "true"}[i] {
			t.Fatal("actual Source Hook identity/state", line)
		}
	}
	if e = m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
}
