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
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualPublicClearPreservesRunningAgentNewRecordHeaderAndTaskOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var roots, children atomic.Int32
	var mu sync.Mutex
	var headers []string
	var artifact string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "CLEAR_LIVE_CHILD_PROMPT") && !strings.Contains(string(body), "ROOT_CLEAR_AGENT_REQUEST") {
			call := children.Add(1)
			mu.Lock()
			headers = append(headers, r.Header.Get("X-Claude-Code-Session-Id"))
			mu.Unlock()
			if call == 1 {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				response(w, "clear-child-bash", "Bash", map[string]any{"command": "printf CHILD_PHYSICAL_AFTER_CLEAR > '" + artifact + "'", "description": "actual preserved clear child side effect"}, "")
				return
			}
			response(w, "clear-child-finish", "", nil, "CHILD_AFTER_CLEAR_COMPLETED")
			return
		}
		if roots.Add(1) == 1 {
			response(w, "clear-root-agent", "Agent", map[string]any{"description": "actual clear retained child", "subagent_type": "general-purpose", "prompt": "CLEAR_LIVE_CHILD_PROMPT Execute one real Bash then report completion", "run_in_background": true}, "")
			return
		}
		response(w, "clear-root-finish", "", nil, "ROOT_CLEAR_FOREGROUND_COMPLETED")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.ForegroundShellProgress = true
	o.Arguments.Tools = []string{"Agent,Bash,TaskOutput"}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_CODE_TMPDIR"] = o.Native.Temp
	}
	artifact = filepath.Join(o.WorkDir, "actual-child-clear-artifact")
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	oldSID := s.process.Controller.SessionID()
	if err = s.Submit(ctx, tui.Input{Text: "ROOT_CLEAR_AGENT_REQUEST", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("Actual background child did not reach model", ctx.Err())
	}
	var task protocol.JSONObject
	if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		tasks, e := s.process.Controller.BackgroundTasksOnOwner(scope)
		if e != nil {
			return e
		}
		for _, candidate := range tasks {
			if candidate.String("type") == "local_agent" {
				task = candidate
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if task.String("status") != "running" || string(task.Get("isBackgrounded")) != "true" || task.String("agentId") == "" {
		t.Fatal("Real background task missing", task)
	}
	oldTarget, err := os.Readlink(task.String("outputFile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(oldTarget, string(filepath.Separator)+oldSID+string(filepath.Separator)) {
		t.Fatal(oldTarget, oldSID)
	}
	deadline := time.Now().Add(2 * time.Second)
	var oldBytes []byte
	for {
		oldBytes, err = os.ReadFile(oldTarget)
		if err == nil && bytes.Contains(oldBytes, []byte("CLEAR_LIVE_CHILD_PROMPT")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Actual initial child record not materialized", err, string(oldBytes))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = s.Submit(ctx, tui.Input{Text: "/clear", Mode: "prompt"}, func(tui.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	newSID := s.process.Controller.SessionID()
	if newSID == oldSID {
		t.Fatal(newSID)
	}
	releaseOnce.Do(func() { close(release) })
	var join func(context.Context) error
	if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, e := s.process.Controller.BackgroundTaskOnOwner(scope, task.String("id"))
		if e != nil {
			return e
		}
		if entry == nil {
			t.Fatal("Clear removed running background ownership")
		}
		join = entry.Join
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = join(ctx); err != nil {
		t.Fatal("Existing child Owner did not finish", err)
	}
	if data, e := os.ReadFile(artifact); e != nil || string(data) != "CHILD_PHYSICAL_AFTER_CLEAR" {
		t.Fatal("Actual child Bash consumer failed", e, string(data))
	}
	newTarget, err := os.Readlink(task.String("outputFile"))
	if err != nil || newTarget == oldTarget || !strings.Contains(newTarget, string(filepath.Separator)+newSID+string(filepath.Separator)) {
		t.Fatal("Real task symlink not retargeted", oldTarget, newTarget, err)
	}
	newBytes, err := os.ReadFile(newTarget)
	if err != nil || !bytes.Contains(newBytes, []byte("CHILD_AFTER_CLEAR_COMPLETED")) {
		t.Fatal("Actual child new transcript absent", err, string(newBytes))
	}
	for _, line := range bytes.Split(newBytes, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		entry := obj(line)
		if entry.String("type") == "assistant" || entry.String("type") == "user" {
			if entry.String("sessionId") != newSID || entry.String("agentId") != task.String("agentId") {
				t.Fatal("Actual child record stamped old parent", string(line))
			}
		}
	}
	afterOld, err := os.ReadFile(oldTarget)
	if err != nil || !bytes.Equal(oldBytes, afterOld) {
		t.Fatal("Already admitted old child transcript changed", err, string(afterOld))
	}
	mu.Lock()
	actualHeaders := append([]string{}, headers...)
	mu.Unlock()
	if len(actualHeaders) != 2 || actualHeaders[0] != oldSID || actualHeaders[1] != newSID {
		t.Fatal("Actual next child request header lost parent bootstrap", actualHeaders, oldSID, newSID)
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal("Physical child shutdown not joined", err)
	}
}
