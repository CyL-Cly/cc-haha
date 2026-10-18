package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualTerminalConversationTagAntGateSameTranscriptToggleAndCancel(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		response(w, "tag-context", "", nil, "actual persisted tag context")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if err := session.Submit(context.Background(), tui.Input{Text: "create actual current transcript"}, nil); err != nil {
		t.Fatal(err)
	}
	var notice string
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.Kind == tui.Notice {
			notice = event.Text
		}
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	if handled, err := session.InvokeTerminalCommand(context.Background(), "tag", "disabled", emit); err != nil || handled {
		t.Fatal("external Source profile admitted internal tag", handled, err)
	}
	// The original command's trusted host USER_TYPE boundary is dynamic. This
	// fixture changes only that exact host value after creating the real history.
	session.native.Environment["USER_TYPE"] = "ant"
	invoke := func(arg string) {
		t.Helper()
		handled, err := session.InvokeTerminalCommand(context.Background(), "tag", arg, emit)
		if err != nil || !handled {
			t.Fatal("actual ant tag entry", arg, handled, err)
		}
	}
	invoke("  Ｔａｇ\u200b  ")
	if notice != "Tagged session with #Tag" {
		t.Fatal("tag sanitizer did not reach actual producer", notice)
	}
	invoke("Tag")
	if panel == nil || panel.Kind != "tag" || panel.Description != "Current tag: #Tag" {
		t.Fatal("actual same tag did not confirm removal", panel)
	}
	oldID := panel.ID
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: oldID, Action: "close"}, emit); err != nil || notice != "Kept tag #Tag" {
		t.Fatal("Escape did not retain actual tag", err, notice)
	}
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: oldID, Action: "select", Row: "yes"}, emit); err == nil {
		t.Fatal("closed tag picker retained live action identity")
	}
	invoke("Tag")
	if err := session.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "select", Row: "yes"}, emit); err != nil || notice != "Removed tag #Tag" {
		t.Fatal("confirmed tag did not persist empty sentinel", err, notice)
	}
	invoke("info")
	if notice != "Tagged session with #info" {
		t.Fatal("non-Source info alias swallowed tag name", notice)
	}
	invoke("list")
	if !strings.HasPrefix(notice, "Usage: /tag <tag-name>") {
		t.Fatal("actual Source info argument did not show help", notice)
	}
	invoke("\u200b")
	if notice != "Tag name cannot be empty" {
		t.Fatal("sanitized empty tag accepted", notice)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", o.Arguments.SessionID+".jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal("actual tag transcript missing", err, files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(data), `"type":"tag","tag":"Tag"`) || !strings.Contains(string(data), `"type":"tag","tag":""`) || !strings.Contains(string(data), `"type":"tag","tag":"info"`) || calls.Load() != 1 {
		t.Fatal("tag actions diverged from actual transcript or opened model", err, string(data), calls.Load())
	}
}
