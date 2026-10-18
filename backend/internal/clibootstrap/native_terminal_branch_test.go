package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualTerminalBranchCopiesPhysicalParentSwitchesSameHarnessAndNextRequest(t *testing.T) {
	var mu sync.Mutex
	var requests [][]byte
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		emitPlanFixture(w, 1, "", "ACTUAL_BRANCH_ASSISTANT")
	}))
	defer peer.Close()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-owned-terminal-branch"
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["NO_PROXY"] = "127.0.0.1,localhost"
	seedNativeReleaseWarmFixture(t, f, "fixture")
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Interactive: true, ForegroundShellProgress: true, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual branch shared harness", Tools: []string{""}, Sources: DefaultState(), MaxTurns: 5, PermissionMode: "default"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := func(text string) protocol.JSONObject {
		return objMain(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}}))
	}
	if err = m.RunTurn(ctx, user("ACTUAL_BRANCH_PARENT_HUMAN")); err != nil {
		t.Fatal(err)
	}
	oldSID, oldOwner, oldController, oldHost := m.Controller.SessionID(), m.Owner, m.Controller, m.Host
	var parentPath string
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		parentPath, err = m.Controller.Writer().ComputedTranscriptPath(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Observe natural physical append completion. No Flush, sorter or writer
	// mutation is inserted into the branch production path.
	var parent []byte
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		parent, err = os.ReadFile(parentPath)
		if err == nil && bytes.Contains(parent, []byte("ACTUAL_BRANCH_ASSISTANT")) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual parent append did not complete", err)
		case <-ticker.C:
		}
	}
	result, err := m.TerminalBranch(ctx, "  original branch \ufeff ")
	if err != nil {
		t.Fatal(err)
	}
	outputText := result.String("text")
	if err := m.CompleteTerminalLocalJSX(ctx, "branch", "original branch", &outputText, "system", false); err != nil {
		t.Fatal("actual branch onDone canonical producer", err)
	}
	newSID := result.String("sessionId")
	if newSID == "" || newSID == oldSID || m.Controller.SessionID() != newSID || m.Owner != oldOwner || m.Controller != oldController || m.Host != oldHost {
		t.Fatal("branch replaced graph or did not switch", result)
	}
	if result.String("text") != "Branched conversation \"original branch\". You are now in the branch.\nTo resume the original: claude -r "+oldSID {
		t.Fatal("actual Source completion differs", result.String("text"))
	}
	forkPath := filepath.Join(filepath.Dir(parentPath), newSID+".jsonl")
	fork, err := os.ReadFile(forkPath)
	if err != nil || !bytes.Contains(fork, []byte("ACTUAL_BRANCH_PARENT_HUMAN")) || !bytes.Contains(fork, []byte("ACTUAL_BRANCH_ASSISTANT")) || !bytes.Contains(fork, []byte(`"forkedFrom"`)) {
		t.Fatal("physical branch missing", err, string(fork))
	}
	if err = m.RunTurn(ctx, user("ACTUAL_BRANCH_NEXT_HUMAN")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(requests) != 2 || !bytes.Contains(requests[1], []byte("ACTUAL_BRANCH_PARENT_HUMAN")) || !bytes.Contains(requests[1], []byte("ACTUAL_BRANCH_NEXT_HUMAN")) {
		t.Error("next real canonical model lost branched context", len(requests))
	}
	mu.Unlock()
	if current, err := os.ReadFile(parentPath); err != nil || !bytes.Equal(parent, current) {
		t.Fatal("original transcript changed across branch/next ASK", err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		key, err := m.originalProjectConfigKey(scope)
		if err != nil {
			return err
		}
		project, err := m.Global.GetProject(key)
		if err != nil {
			return err
		}
		if project.String("lastSessionId") != oldSID {
			t.Errorf("old costs saved under wrong identity: %s", rawMain(project))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The live /resume caller loads its actual lite selection before invoking
	// the same REPL callback, rather than reusing headless BeginActivate.
	selected, message, err := m.ResolveTerminalResumeArgument(ctx, oldSID)
	if err != nil || message != "" || selected == nil {
		t.Fatal("ordinary UUID selection", err, message)
	}
	catalog, err := m.ReadTerminalResumeCatalog(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var menu []protocol.JSONObject
	_ = json.Unmarshal(catalog.Presentation().Get("logs"), &menu)
	for _, row := range menu {
		if terminalLogSID(row) == newSID {
			t.Fatal("active branch leaked into resume picker")
		}
	}
	for i, row := range menu {
		if terminalLogSID(row) == oldSID {
			if err := m.RenameTerminalResume(ctx, catalog, i, "native owned rename"); err != nil {
				t.Fatal("actual picker title producer", err)
			}
			current, err := os.ReadFile(parentPath)
			if err != nil || !bytes.Contains(current, []byte("native owned rename")) {
				t.Fatal("picker failed actual captured-path write", err)
			}
		}
	}
	resumed, err := m.ResumeTerminalSelection(ctx, selected)
	if err != nil || resumed.String("sessionId") != oldSID || m.Owner != oldOwner || m.Controller != oldController || m.Host != oldHost {
		t.Fatal("live resume changed canonical graph", err, resumed)
	}
	if err = m.RunTurn(ctx, user("ACTUAL_RESUMED_PARENT_NEXT_HUMAN")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(requests) != 3 || !bytes.Contains(requests[2], []byte("ACTUAL_BRANCH_PARENT_HUMAN")) || !bytes.Contains(requests[2], []byte("ACTUAL_RESUMED_PARENT_NEXT_HUMAN")) || bytes.Contains(requests[2], []byte("ACTUAL_BRANCH_NEXT_HUMAN")) {
		t.Error("actual resumed next request used wrong history", len(requests))
	}
	mu.Unlock()
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.Host.CheckTerminalResumeOnOwner(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign transition admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var rows []protocol.JSONObject
	if err = json.Unmarshal(result.Get("history"), &rows); err != nil || len(rows) < 2 {
		t.Fatal("branch UI history missing", err, result)
	}
}
