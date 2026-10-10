package application

import (
	"bytes"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestThinkingPickerChangesNextActualHarnessRestoresLegacyBudgetAndNeverWritesGlobal(t *testing.T) {
	requests := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		response(w, "thinking-done", "", nil, "ACTUAL_THINKING_TURN")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Model = "claude-sonnet-4-6"
	budget := 2048.0
	o.Arguments.Thinking = requestcontext.Thinking{Type: "enabled", BudgetTokens: &budget}
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	global := filepath.Join(o.Native.Home, ".claude.json")
	before, _ := os.ReadFile(global)
	current := tui.ThinkingPicker{}
	emit := func(event tui.Event) error {
		if event.ThinkingPicker != nil {
			current = *event.ThinkingPicker
		}
		return nil
	}
	if e = s.InvokeAction(context.Background(), "chat:thinkingToggle", emit); e != nil {
		t.Fatal(e)
	}
	if !current.Current || current.MidConversation {
		t.Fatal("wrong actual initial state", current)
	}
	if e = s.RespondThinkingPicker(context.Background(), current.ID, tui.ThinkingPickerResult{Enabled: false}, emit); e != nil {
		t.Fatal(e)
	}
	if e = s.Submit(context.Background(), tui.Input{TurnID: "off", Text: "Respond with thinking off"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.InvokeAction(context.Background(), "chat:thinkingToggle", emit); e != nil {
		t.Fatal(e)
	}
	if current.Current || !current.MidConversation {
		t.Fatal("wrong actual ongoing state", current)
	}
	if e = s.RespondThinkingPicker(context.Background(), current.ID, tui.ThinkingPickerResult{Enabled: true}, emit); e != nil {
		t.Fatal(e)
	}
	thinking, _, e := s.process.TerminalThinking(context.Background())
	if e != nil || thinking.Type != "enabled" || thinking.BudgetTokens == nil || *thinking.BudgetTokens != budget {
		t.Fatal("legacy budget not restored", thinking, e)
	}
	if e = s.Submit(context.Background(), tui.Input{TurnID: "on", Text: "Respond with original thinking budget"}, nil); e != nil {
		t.Fatal(e)
	}
	if len(requests) != 2 {
		t.Fatal("picker performed model inference", len(requests))
	}
	for i, body := range requests {
		request := obj([]byte(body))
		config, _ := request.Object("thinking")
		if i == 0 && request.Get("thinking") != nil && config.String("type") != "disabled" || i == 1 && (config.String("type") != "enabled" || string(config.Get("budget_tokens")) != "2048") {
			t.Fatal("next actual provider config wrong", i, config)
		}
	}
	after, _ := os.ReadFile(global)
	if !bytes.Equal(before, after) {
		t.Fatal("thinking picker wrote global config")
	}
	if e = s.RespondThinkingPicker(context.Background(), current.ID, tui.ThinkingPickerResult{Enabled: false}, nil); e == nil {
		t.Fatal("stale callback changed session")
	}
}

func TestThinkingPickerForeignOwnerAndCancelKeepActualOriginalConfig(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Arguments.Thinking = requestcontext.Thinking{Type: "disabled"}
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	foreign := cooperative.New()
	defer foreign.Close()
	e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error { return s.process.Host.SetTerminalThinking(scope, true) })
	if !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("foreign owner changed session thinking", e)
	}
	e = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _, e := s.process.Host.TerminalThinking(scope)
		return e
	})
	if !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("foreign owner read session thinking", e)
	}
	var props tui.ThinkingPicker
	emit := func(event tui.Event) error {
		if event.ThinkingPicker != nil {
			props = *event.ThinkingPicker
		}
		return nil
	}
	if e = s.InvokeThinkingPicker(context.Background(), emit); e != nil {
		t.Fatal(e)
	}
	if e = s.RespondThinkingPicker(context.Background(), props.ID, tui.ThinkingPickerResult{Cancelled: true, Enabled: true}, nil); e != nil {
		t.Fatal(e)
	}
	config, _, e := s.process.TerminalThinking(context.Background())
	if e != nil || config.Type != "disabled" {
		t.Fatal("cancel mutated shared state", config, e)
	}
}

func TestTerminalThinkingSnapshotIsDetachedAndClosedOwnerRejects(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	budget := 8192.0
	o.Arguments.Thinking = requestcontext.Thinking{Type: "enabled", BudgetTokens: &budget}
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	value, _, e := s.process.TerminalThinking(context.Background())
	if e != nil || value.BudgetTokens == nil {
		t.Fatal("owned snapshot missing", e)
	}
	*value.BudgetTokens = 1
	value, _, e = s.process.TerminalThinking(context.Background())
	if e != nil || *value.BudgetTokens != budget {
		t.Fatal("external snapshot changed actual Host config", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = s.process.SetTerminalThinking(ctx, false); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled setter changed actual state", e)
	}
	value, _, e = s.process.TerminalThinking(context.Background())
	if e != nil || value.Type != "enabled" || *value.BudgetTokens != budget {
		t.Fatal("cancelled setter lost actual config", e)
	}
	if e = s.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.process.TerminalThinking(context.Background()); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal("closed owner accepted snapshot", e)
	}
	if e = s.process.SetTerminalThinking(context.Background(), true); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal("closed owner accepted setter", e)
	}
}
