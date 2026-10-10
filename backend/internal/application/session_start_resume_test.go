package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
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

func TestActualSessionStartResumeSameIdentityAppendsNewHookHistoryOnce(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		want := "ACTUAL_STARTUP_CONTEXT_R21"
		if n == 2 {
			want = "ACTUAL_RESUME_CONTEXT_R21"
		}
		if !strings.Contains(string(body), want) {
			t.Error("original retained SessionStart context absent", want)
		}
		response(w, "resume-hook-physical", "", nil, "REAL_SESSION_TURN_R21")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(o.WorkDir, "startup-resume-hook-input.jsonl")
	hooks := []any{}
	for _, trigger := range []string{"startup", "resume"} {
		contextMarker := "ACTUAL_STARTUP_CONTEXT_R21"
		if trigger == "resume" {
			contextMarker = "ACTUAL_RESUME_CONTEXT_R21"
		}
		command := `cat >> "` + journal + `"; printf '\n' >> "` + journal + `"; printf '%s' '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"` + contextMarker + `"}}'`
		hooks = append(hooks, map[string]any{"matcher": trigger, "hooks": []any{map[string]any{"type": "command", "command": command}}})
	}
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), raw(map[string]any{"hooks": map[string]any{"SessionStart": hooks}}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	first, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	sid := first.process.Controller.SessionID()
	if err = first.Submit(ctx, tui.Input{TurnID: "first", Text: "first original user"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	o.Arguments.Resume = sid
	o.Arguments.SessionID = ""
	second, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	if second.process.Controller.SessionID() != sid {
		t.Fatal("Source resume changed actual SID")
	}
	if err = second.Submit(ctx, tui.Input{TurnID: "second", Text: "next original user"}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("extra provider loop", calls.Load())
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 2 {
		t.Fatal("duplicate startup/resume hook", string(data))
	}
	for i, line := range lines {
		input := obj([]byte(line))
		if input.String("source") != []string{"startup", "resume"}[i] || input.String("session_id") != sid {
			t.Fatal("actual original SID/trigger", line)
		}
	}
	history := second.process.Controller.History()
	found := false
	for _, row := range history {
		attachment, _ := row.Object("attachment")
		if strings.Contains(string(attachment.Get("content")), "ACTUAL_RESUME_CONTEXT_R21") {
			found = true
		}
	}
	if !found {
		t.Fatal("resume hook not appended to actual same history")
	}
}
