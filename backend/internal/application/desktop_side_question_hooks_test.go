package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualIdleSideQuestionSubagentStopModelsDoNotHoldMain(t *testing.T) {
	for _, kind := range []string{"prompt", "agent"} {
		t.Run(kind, func(t *testing.T) {
			hookEntered := make(chan struct{})
			releaseHook := make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(releaseHook) })
			var calls, hookCalls atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				calls.Add(1)
				hook := bytes.Contains(data, []byte("You are evaluating a hook in Claude Code")) || bytes.Contains(data, []byte("You are verifying a stop condition in Claude Code"))
				if hook {
					if hookCalls.Add(1) != 1 {
						t.Error("unexpected extra hook model")
					}
					close(hookEntered)
					select {
					case <-releaseHook:
					case <-r.Context().Done():
						return
					}
					if kind == "agent" {
						response(w, "hook-structured", "StructuredOutput", map[string]any{"ok": true}, "")
					} else {
						response(w, "hook-prompt", "", nil, `{"ok":true}`)
					}
					return
				}
				if bytes.Contains(data, []byte("This is a side question from the user")) {
					response(w, "side", "", nil, "IDLE SIDE ANSWER")
					return
				}
				response(w, "main", "", nil, "PARENT HOOK SAFE")
			}))
			defer model.Close()
			o := nativeSessionOptions(t, model.URL)
			o.Arguments.Tools = nil
			delete(o.Environment, "CLAUDE_CODE_SIMPLE")
			o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			if e := os.MkdirAll(config, 0700); e != nil {
				t.Fatal(e)
			}
			settings := map[string]any{"hooks": map[string]any{"SubagentStop": []any{map[string]any{"hooks": []any{map[string]any{"type": kind, "prompt": "Confirm the side answer is complete", "model": "fixture-model"}}}}}}
			if e := os.WriteFile(filepath.Join(config, "settings.json"), raw(settings), 0600); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			defer cancel()
			s, e := NewSession(ctx, o)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			sideDone := make(chan error, 1)
			go func() {
				answer, e := s.process.SideQuestionControl(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: raw("side_question")}, protocol.JSONField{Name: "question_id", Value: raw("11111111-1111-4111-8111-111111111111")}, protocol.JSONField{Name: "question", Value: raw("side without any main turn")}, protocol.JSONField{Name: "history", Value: raw([]any{})}))
				if e == nil && answer.String("response") != "IDLE SIDE ANSWER" {
					e = io.ErrUnexpectedEOF
				}
				sideDone <- e
			}()
			select {
			case <-hookEntered:
			case e := <-sideDone:
				t.Fatal("side hook skipped or failed before actual model", e)
			case <-ctx.Done():
				t.Fatal("idle hook never reached model", ctx.Err())
			}
			mainDone := make(chan error, 1)
			go func() {
				mainDone <- s.Submit(ctx, tui.Input{TurnID: "first-main", Text: "main while side hook is held"}, nil)
			}()
			select {
			case e := <-mainDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(3 * time.Second):
				t.Error("main completion held behind side SubagentStop model")
			}
			release.Do(func() { close(releaseHook) })
			select {
			case e := <-sideDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if calls.Load() != 3 || hookCalls.Load() != 1 {
				t.Fatal("physical main/side/hook counts", calls.Load(), hookCalls.Load())
			}
			receipt, e := s.process.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error {
				data := raw(s.process.Controller.History())
				if bytes.Contains(data, []byte("IDLE SIDE ANSWER")) || bytes.Contains(data, []byte("SubagentStop")) {
					return io.ErrUnexpectedEOF
				}
				return nil
			})
			if e == nil {
				e = receipt.Wait()
			}
			if e != nil {
				t.Fatal("fork changed main canonical", e)
			}
		})
	}
}
func TestActualSideQuestionSubagentStopCommandUsesSideIdentityAndNoTranscript(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		response(w, "side", "", nil, "COMMAND SIDE ANSWER")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = nil
	delete(o.Environment, "CLAUDE_CODE_SIMPLE")
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(o.WorkDir, "subagent-stop-input.json")
	script := filepath.Join(o.WorkDir, "hook.sh")
	if e := os.WriteFile(script, []byte("#!/bin/sh\ncat > '"+marker+"'\nprintf '{}'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "settings.json"), raw(map[string]any{"hooks": map[string]any{"SubagentStop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/bin/sh '" + script + "'"}}}}}}), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, e := NewSession(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	answer, e := s.process.SideQuestionControl(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: raw("side_question")}, protocol.JSONField{Name: "question_id", Value: raw("11111111-1111-4111-8111-111111111111")}, protocol.JSONField{Name: "question", Value: raw("side command")}, protocol.JSONField{Name: "history", Value: raw([]any{})}))
	if e != nil {
		t.Fatal(e)
	}
	if answer.String("response") != "COMMAND SIDE ANSWER" {
		t.Fatal(answer)
	}
	data, e := os.ReadFile(marker)
	if e != nil {
		t.Fatal("actual command not executed", e)
	}
	input := obj(data)
	agentID := input.String("agent_id")
	if input.String("hook_event_name") != "SubagentStop" || len(agentID) != 17 || !strings.HasPrefix(agentID, "a") || input.String("last_assistant_message") != "COMMAND SIDE ANSWER" {
		t.Fatal("original SubagentStop input", input)
	}
	if !strings.HasSuffix(input.String("agent_transcript_path"), "/subagents/agent-"+agentID+".jsonl") {
		t.Error("ephemeral fork path must be Source child path", input.String("agent_transcript_path"))
	}
	if _, e := os.Stat(input.String("agent_transcript_path")); !os.IsNotExist(e) {
		t.Fatal("side transcript unexpectedly persisted", e)
	}
}
