package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualGoalAuxiliaryHTTPContinuesThenCompletesOnSharedHarness(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "fixture-evaluator"
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := int(calls.Add(1))
		switch n {
		case 1:
			emitWorktreeModel(w, n, "", "", "FIRST_MAIN_NOT_DONE_582")
		case 2:
			if !bytes.Contains(body, []byte("fixture-evaluator")) || !bytes.Contains(body, []byte("You are evaluating a hook")) || !bytes.Contains(body, []byte("FIRST_MAIN_NOT_DONE_582")) || !bytes.Contains(body, []byte(`"name":"Read"`)) {
				t.Errorf("actual auxiliary request missing history/model/system: %.2800s", body)
			}
			emitWorktreeModel(w, n, "", "", `{"ok":false,"reason":"Finish PRIVATE_GOAL_582 <then> & verify"}`)
		case 3:
			if !bytes.Contains(body, []byte("Stop hook feedback:")) || bytes.Contains(body, []byte("You are evaluating a hook")) {
				t.Errorf("continuation input wrong %.2800s", body)
			}
			emitWorktreeModel(w, n, "", "", "SECOND_MAIN_DONE_582")
		case 4:
			emitWorktreeModel(w, n, "", "", `{"ok":true}`)
		case 5:
			emitWorktreeModel(w, n, "", "", "NEXT_NORMAL_TURN_582")
		default:
			t.Errorf("unexpected model call%d", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var output, diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual goal shared Harness", Tools: []string{"Read"}, MaxTurns: 6, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: &output, OnOutput: func(data []byte) error { output.Write(data); output.WriteByte(10); return nil }, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
	}
	if err = m.RunTurn(ctx, user("/goal")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 0 {
		t.Fatal("usage invoked model")
	}
	if err = m.RunTurn(ctx, user("/goal PRIVATE_GOAL_582")); err != nil {
		t.Fatal(err, diagnostics.String(), output.String())
	}
	if calls.Load() != 4 {
		t.Fatal("real evaluator/continuation sequence", calls.Load(), output.String())
	}
	usage, err := m.ReadSessionUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var tokens float64
	_ = json.Unmarshal(usage.Get("totalInputTokens"), &tokens)
	if tokens != 4 {
		t.Fatal("actual auxiliary/main accounting missing", usage)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		state, e := m.nativeGoalState(scope)
		if e != nil {
			return e
		}
		goal, e := state.Get(scope, m.Controller.SessionID())
		if e != nil {
			return e
		}
		if goal != nil {
			return fmt.Errorf("successful evaluator retained goal")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("Goal marked complete.")) || !bytes.Contains(output.Bytes(), []byte("Goal continuing: Finish PRIVATE_GOAL_582 then verify")) {
		t.Fatal("actual SDK status absent", output.String())
	}
	if err = m.RunTurn(ctx, user("Continue normally")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 5 {
		t.Fatal("completed hook was re-run", calls.Load())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	disk, e := os.ReadFile(files[0])
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(disk, []byte("Goal marked complete.")) || bytes.Contains(disk, []byte("You are evaluating a hook")) || bytes.Contains(disk, []byte(`\\"ok\\":true`)) {
		t.Fatal("auxiliary leaked or actual status absent from canonical", string(disk))
	}
}

func TestActualGoalAuxiliaryHTTPParentAbortJoinsTransportAndRetainsGoal(t *testing.T) {
	f := worktreeMainFixture(t)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	started, closed := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			emitWorktreeModel(w, 1, "", "", "MAIN_BEFORE_ABORT_397")
			return
		}
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual goal abort", Tools: []string{"Read"}, MaxTurns: 3, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	turn, abort := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(turn, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "/goal preserve real target after cancellation"})}))
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("real evaluator not started", diagnostics.String())
	}
	abort()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("HTTP transport not canceled")
	}
	select {
	case err = <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal("parent abort became capability/error", err)
		}
	case <-ctx.Done():
		t.Fatal("native auxiliary finally did not settle")
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		state, e := m.nativeGoalState(scope)
		if e != nil {
			return e
		}
		goal, e := state.Get(scope, m.Controller.SessionID())
		if e != nil {
			return e
		}
		if goal == nil {
			return fmt.Errorf("user abort incorrectly marked goal complete")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("parent abort continued goal", calls.Load())
	}
}
