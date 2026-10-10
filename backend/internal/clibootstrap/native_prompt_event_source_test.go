package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeGenericPromptHookSourcePromiseCompletionAndUserConsumer(t *testing.T) {
	for _, answer := range []string{`{"ok":true}`, `{"ok":false,"reason":"actual unmet"}`, `{"ok":false,"reason":"\ud800"}`, `{"ok":false}`, `not JSON`, `{"ok":"yes"}`, `{"ok":true,"future":12}`} {
		t.Run(answer, func(t *testing.T) {
			auxiliary, main := atomic.Int32{}, atomic.Int32{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Content-Type") == "" {
					t.Error("physical model request missing content type")
				}
				var request protocol.JSONObject
				_ = json.NewDecoder(r.Body).Decode(&request)
				if strings.Contains(string(rawMain(request.Get("system"))), "You are evaluating a hook in Claude Code") {
					auxiliary.Add(1)
					emitPlanFixture(w, 1, "", answer)
				} else {
					main.Add(1)
					emitPlanFixture(w, 2, "", "PROMPT_SOURCE_DONE")
				}
			}))
			defer peer.Close()
			hook := map[string]any{"type": "prompt", "prompt": "Check original input $ARGUMENTS", "model": "fixture-model", "statusMessage": "actual prompt display"}
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": []any{hook}}}}}), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) {
				o.Arguments.Tools = []string{"Read,Write"}
				o.Arguments.PermissionMode = "bypassPermissions"
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			inputID, content := "source-prompt-input", quoteMain("actual source prompt input")
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(inputID)}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: content}))})); err != nil {
				t.Fatal(err)
			}
			defer m.ShutdownSession(context.Background())
			if auxiliary.Load() != 1 {
				t.Fatal("actual prompt auxiliary count", auxiliary.Load())
			}
			var nativeMessages []protocol.JSONObject
			var base protocol.JSONObject
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				for _, value := range m.Controller.History() {
					if value.String("type") == "assistant" {
						break
					}
					nativeMessages = append(nativeMessages, value.Clone())
				}
				path, err := m.Controller.Writer().ComputedTranscriptPath(scope)
				base = protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "transcript_path", Value: quoteMain(path)}, protocol.JSONField{Name: "cwd", Value: quoteMain(m.state.Snapshot().CWD)})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				return
			}
			home := t.TempDir()
			temp := filepath.Join(home, "tmp")
			if err := os.MkdirAll(temp, 0700); err != nil {
				t.Fatal(err)
			}
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-user-prompt.fixture.ts")
			row := protocol.NewJSONObject(protocol.JSONField{Name: "base", Value: rawMain(base)}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "inputUUID", Value: quoteMain(inputID)}, protocol.JSONField{Name: "permissionMode", Value: quoteMain("bypassPermissions")}, protocol.JSONField{Name: "output", Value: rawMain(map[string]any{"type": "prompt", "hook": hook, "response": answer})})
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "rows": []protocol.JSONObject{row}}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var source struct {
				Rows []struct {
					Result struct {
						Messages    []protocol.JSONObject
						ShouldQuery bool
					}
					PromptCapture struct {
						Messages []protocol.JSONObject
					}
				}
			}
			if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != 1 {
				t.Fatal(err, string(out))
			}
			if source.Rows[0].Result.ShouldQuery != (main.Load() > 0) || len(source.Rows[0].PromptCapture.Messages) != 1 {
				t.Fatal("Source shouldQuery/undefined history", source.Rows[0].Result.ShouldQuery, main.Load(), string(out))
			}
			actual, want := rawMain(normalizedUserPromptMessages(nativeMessages)), rawMain(normalizedUserPromptMessages(source.Rows[0].Result.Messages))
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
				t.Errorf("Source prompt actor promise/consumer differs\nGo%s\nSource%s", actual, want)
			}
		})
	}
}
