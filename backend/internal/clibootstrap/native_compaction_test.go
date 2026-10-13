package clibootstrap

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualNativeCompactUsesPhysicalModelAndContinuesCanonicalHistory(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		switch n {
		case 1:
			emitPlanFixture(w, 1, "", "BEFORE_SUMMARY_DECISION")
		case 2:
			if !strings.Contains(string(body), "CRITICAL: Respond with TEXT ONLY") || !strings.Contains(string(body), "BEFORE_SUMMARY_DECISION") {
				t.Error("compact model missing real history/instruction", string(body))
			}
			emitPlanFixture(w, 2, "", "<summary>COMPACT_TASK_STATE_保留用户约束</summary>")
		case 3:
			if !strings.Contains(string(body), "COMPACT_TASK_STATE_保留用户约束") || strings.Contains(string(body), "BEFORE_SUMMARY_DECISION") {
				t.Error("continued query did not use compacted history", string(body))
			}
			emitPlanFixture(w, 3, "", "CONTINUED_AFTER_COMPACT")
		default:
			t.Error("unexpected physical request", n)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { n.Environment["DISABLE_AUTO_COMPACT"] = "1" })
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for _, text := range []string{"Remember my requested implementation and pending work.", "/compact preserve the Chinese user constraint", "Continue the original task."} {
		user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
		if e := m.RunTurn(ctx, user); e != nil {
			t.Fatal("actual compact CLI", e)
		}
	}
	if calls.Load() != 3 {
		t.Fatal("manual compact was not a physical model request", calls.Load())
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		history := m.Controller.History()
		if len(history) < 4 {
			t.Fatal(history)
		}
		boundary := false
		for _, record := range history {
			boundary = boundary || record.String("subtype") == "compact_boundary"
		}
		if !boundary {
			t.Fatal("canonical controller not replaced")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestActualNativeAutoCompactRunsBeforeModelAndContinuesSameTurn(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		if n == 1 {
			if !strings.Contains(string(body), "CRITICAL: Respond with TEXT ONLY") {
				t.Error("threshold failed to execute physical compaction")
			}
			emitPlanFixture(w, 1, "", "<summary>AUTO_COMPACT_REAL_TASK</summary>")
		} else if n == 2 {
			if !strings.Contains(string(body), "AUTO_COMPACT_REAL_TASK") {
				t.Error("auto resumed history missing summary", string(body))
			}
			emitPlanFixture(w, 2, "", "SAME_TURN_CONTINUED")
		} else {
			t.Error("auto compaction recursed", n)
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = "8192"
		n.Environment["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"] = "1"
		n.Environment["CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE"] = "1000000"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": strings.Repeat("preserve original requested task. ", 100)})})
	if e := m.RunTurn(ctx, user); e != nil {
		t.Fatal("actual auto compact", e)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}

func TestActualNativeCompactCancellationDoesNotReplaceHistory(t *testing.T) {
	pending := make(chan struct{})
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			emitPlanFixture(w, 1, "", "UNCHANGED_ORIGINAL_TASK")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(pending)
		<-r.Context().Done()
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { n.Environment["DISABLE_AUTO_COMPACT"] = "1" })
	initial := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Initial task must survive canceled compaction"})})
	if e := m.RunTurn(context.Background(), initial); e != nil {
		t.Fatal(e)
	}
	before := []byte{}
	if e := m.Owner.Run(context.Background(), func(*cooperative.Scope) error { before = rawMain(m.Controller.History()); return nil }); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "/compact"})})
		done <- m.RunTurn(ctx, user)
	}()
	select {
	case <-pending:
		cancel()
	case <-ctx.Done():
		t.Fatal("summary request didn't start")
	}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("canceled summary reported success")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("compact cancellation didn't close physical stream")
	}
	if e := m.Owner.Run(context.Background(), func(*cooperative.Scope) error {
		after := rawMain(m.Controller.History())
		if string(before) != string(after) {
			t.Fatal("canceled compact mutated canonical history", string(after))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("cancel triggered fallback or retry", calls.Load())
	}
}

func TestActualNativePartialCompactOwnerPipelinePersistsAndContinues(t *testing.T) {
	for _, direction := range []string{"from", "up_to"} {
		t.Run(direction, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				n := calls.Add(1)
				if n == 1 {
					emitPlanFixture(w, 1, "", "FIRST_VERBATIM_DECISION")
				} else if n == 2 {
					emitPlanFixture(w, 2, "", "SECOND_VERBATIM_DECISION")
				} else if n == 3 {
					if !strings.Contains(string(body), "CRITICAL: Respond with TEXT ONLY") {
						t.Error("partial didn't use summary model")
					}
					emitPlanFixture(w, 3, "", "<summary>PARTIAL_CONTEXT_STATE</summary>")
				} else if n == 4 {
					if !strings.Contains(string(body), "PARTIAL_CONTEXT_STATE") {
						t.Error("partial summary missing in continuation")
					}
					want := "FIRST_VERBATIM_DECISION"
					if direction == "up_to" {
						want = "SECOND_VERBATIM_DECISION"
					}
					if !strings.Contains(string(body), want) {
						t.Error("partial lost preserved original segment", direction, string(body))
					}
					emitPlanFixture(w, 4, "", "PARTIAL_CONTINUED")
				} else {
					t.Error("unexpected partial model call", n)
				}
			}))
			defer peer.Close()
			m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { n.Environment["DISABLE_AUTO_COMPACT"] = "1" })
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			run := func(text string) {
				user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
				if e := m.RunTurn(ctx, user); e != nil {
					t.Fatal(e)
				}
			}
			run("first user task")
			run("second user task")
			if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				history := m.Controller.History()
				pivot := 0
				for i, record := range history {
					api, _ := record.Object("message")
					if api.String("content") == "second user task" {
						pivot = i
						break
					}
				}
				if pivot == 0 {
					return fmt.Errorf("second input pivot missing")
				}
				return m.Host.CompactHistoryOnOwner(ctx, scope, pivot, direction, "retain explicit constraints")
			}); e != nil {
				t.Fatal("actual partial selector producer", e)
			}
			run("continue partial task")
			if calls.Load() != 4 {
				t.Fatal(calls.Load())
			}
		})
	}
}
