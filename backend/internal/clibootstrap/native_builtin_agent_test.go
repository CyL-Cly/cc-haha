package clibootstrap

import (
	"bytes"
	"context"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualAgentSlashMalformedDoesNotPOSTAndSelectedAgentRunsSameHarness(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	path := filepath.Join(f.cwd, "agent-command-input.txt")
	f.write(t, path, "REAL_AGENT_COMMAND_CHILD_READ_7F4B\n")
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		round := int(calls.Add(1))
		switch round {
		case 1:
			if !bytes.Contains(body, []byte(`Use the Agent tool with subagent_type`)) || !bytes.Contains(body, []byte(`Read the exact private file then report its marker.`)) {
				t.Errorf("actual builtin prompt missing %.1800s", body)
			}
			emitWorktreeModel(w, round, "Agent", `{"prompt":"Read the exact private file then report its marker.","description":"slash selected actual Agent","subagent_type":"general-purpose"}`, "")
		case 2:
			emitWorktreeModel(w, round, "Read", `{"file_path":"agent-command-input.txt"}`, "")
		case 3:
			if !bytes.Contains(body, []byte("REAL_AGENT_COMMAND_CHILD_READ_7F4B")) {
				t.Errorf("actual child did not read physical file %.1800s", body)
			}
			emitWorktreeModel(w, round, "", "", "CHILD_READ_CONFIRMED_7F4B")
		case 4:
			if !bytes.Contains(body, []byte("CHILD_READ_CONFIRMED_7F4B")) {
				t.Errorf("actual child result not delivered to main Harness %.1800s", body)
			}
			emitWorktreeModel(w, round, "", "", "AGENT_SLASH_COMPLETED_7F4B")
		default:
			t.Errorf("duplicate model request %d", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual builtin /agent command main Harness", Tools: []string{"Agent,Read"}, MaxTurns: 6, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := func(content string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": content})})
	}
	if err = m.RunTurn(ctx, user("/agent general-purpose")); err != nil || calls.Load() != 0 {
		t.Fatal("malformed original command queried or failed harness", err, calls.Load(), diagnostics.String())
	}
	if err = m.RunTurn(ctx, user("/agent general-purpose Read the exact private file then report its marker.")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 4 {
		t.Fatal("not actual main→Agent→Read→main task", calls.Load(), diagnostics.String())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	transcripts, err := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if err != nil || len(transcripts) != 1 {
		t.Fatal("canonical main transcript missing", transcripts, err)
	}
	main, err := os.ReadFile(transcripts[0])
	if err != nil || !bytes.Contains(main, []byte("Usage: /agent")) || !bytes.Contains(main, []byte("AGENT_SLASH_COMPLETED_7F4B")) {
		t.Fatal("main records not persisted", err)
	}
	sidechains, err := filepath.Glob(filepath.Join(strings.TrimSuffix(transcripts[0], ".jsonl"), "subagents", "agent-*.jsonl"))
	if err != nil || len(sidechains) != 1 {
		t.Fatal("actual child sidechain missing", sidechains, err)
	}
	child, err := os.ReadFile(sidechains[0])
	if err != nil || !bytes.Contains(child, []byte("REAL_AGENT_COMMAND_CHILD_READ_7F4B")) || !bytes.Contains(child, []byte("CHILD_READ_CONFIRMED_7F4B")) {
		t.Fatal(fmt.Errorf("actual child Read/result not persisted: %w", err))
	}
}
