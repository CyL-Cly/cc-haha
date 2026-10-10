package clibootstrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualGoalColdResumeRestoresCanonicalObjectiveThenClearsOnEvaluation(t *testing.T) {
	f := worktreeMainFixture(t)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	evaluating, transportClosed := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := int(calls.Add(1))
		switch n {
		case 1:
			emitWorktreeModel(w, n, "", "", "INTERRUPTED_GOAL_MAIN_834")
		case 2:
			close(evaluating)
			<-r.Context().Done()
			close(transportClosed)
		case 3:
			emitWorktreeModel(w, n, "", "", "COLD_GOAL_RESUME_MAIN_834")
		case 4:
			if !bytes.Contains(body, []byte("PRIVATE_COLD_GOAL_834")) || !bytes.Contains(body, []byte("COLD_GOAL_RESUME_MAIN_834")) {
				t.Errorf("restored evaluator objective/history absent %.2800s", body)
			}
			emitWorktreeModel(w, n, "", "", `{"ok":true}`)
		default:
			t.Error("extra model calls", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var diagnostics bytes.Buffer
	options := MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual cold restored goal", Tools: []string{"Read"}, MaxTurns: 4, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics}
	m, err := NewMainPrint(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	sid := m.Controller.SessionID()
	turn, abort := context.WithCancel(ctx)
	done := make(chan error, 1)
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
	}
	go func() { done <- m.RunTurn(turn, user("/goal PRIVATE_COLD_GOAL_834")) }()
	select {
	case <-evaluating:
	case <-ctx.Done():
		t.Fatal("evaluator not reached", diagnostics.String())
	}
	abort()
	select {
	case <-transportClosed:
	case <-ctx.Done():
		t.Fatal("transport not canceled")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("turn not settled")
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	options.Arguments.Resume = sid
	resumed, err := NewMainPrint(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resumed.ShutdownSession(context.Background()) })
	if err = resumed.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if resumed.Controller.SessionID() != sid {
		t.Fatal("resume activated wrong SID", sid, resumed.Controller.SessionID())
	}
	if err = resumed.RunTurn(ctx, user("Continue the persisted goal.")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 4 {
		t.Fatal("cold restoration did not run actual evaluator", calls.Load())
	}
	if err = resumed.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		state, e := resumed.nativeGoalState(scope)
		if e != nil {
			return e
		}
		goal, e := state.Get(scope, sid)
		if e != nil {
			return e
		}
		if goal != nil {
			return fmt.Errorf("completed restored goal retained")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
