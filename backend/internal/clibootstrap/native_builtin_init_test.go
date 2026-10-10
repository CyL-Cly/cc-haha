package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestActualInitSlashReadsWritesChecksDocumentAndReusesProjectConfig(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.write(t, filepath.Join(f.cwd, "README.md"), "# private fixture\nBuild with go build ./...\n")
	document := "# CLAUDE.md\n\nThis file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.\n\nBuild with go build ./...\nPRIVATE_INIT_DOC_REAL_19D\n"
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := int(calls.Add(1))
		switch n {
		case 1:
			if !bytes.Contains(body, []byte("Please analyze this codebase and create a CLAUDE.md file")) || bytes.Contains(body, []byte("ARGUMENTS: ignored init args")) {
				t.Errorf("not original builtin init prompt %.1800s", body)
			}
			emitWorktreeModel(w, n, "Read", `{"file_path":"README.md"}`, "")
		case 2:
			if !bytes.Contains(body, []byte("Build with go build ./...")) {
				t.Error("actual README not read")
			}
			input, _ := json.Marshal(map[string]string{"file_path": "CLAUDE.md", "content": document})
			emitWorktreeModel(w, n, "Write", string(input), "")
		case 3:
			emitWorktreeModel(w, n, "Read", `{"file_path":"CLAUDE.md"}`, "")
		case 4:
			if !bytes.Contains(body, []byte("PRIVATE_INIT_DOC_REAL_19D")) {
				t.Error("actual created document not verified")
			}
			emitWorktreeModel(w, n, "", "", "INIT_ACTUAL_SHARED_HARNESS_DONE_19D")
		default:
			t.Error("extra init model turn", n)
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual init shared Harness", Tools: []string{"Read,Write"}, PermissionMode: "bypassPermissions", MaxTurns: 6, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "/init ignored init args"})})
	if err = m.RunTurn(ctx, input); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 4 {
		t.Fatal("not actual Read→Write→Read execution", calls.Load())
	}
	physical, err := os.ReadFile(filepath.Join(f.cwd, "CLAUDE.md"))
	if err != nil || string(physical) != document {
		t.Fatal("actual documentation missing", err)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.nativeMaybeMarkProjectOnboardingComplete(ctx, skillcommands.Invocation{NativeScope: scope}); err != nil {
			return err
		}
		key, err := m.originalProjectConfigKey(scope)
		if err != nil {
			return err
		}
		project, err := m.Global.GetProject(key)
		if err != nil {
			return err
		}
		if string(project.Get("hasCompletedProjectOnboarding")) != "true" {
			return io.ErrUnexpectedEOF
		}
		return nil
	}); err != nil {
		t.Fatal("original onboarding did not use created file/shared config", err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil || !bytes.Contains(disk, []byte("INIT_ACTUAL_SHARED_HARNESS_DONE_19D")) || !bytes.Contains(disk, []byte("<command-name>/init")) {
		t.Fatal("actual command/tool/result canonical records missing", err)
	}
}
