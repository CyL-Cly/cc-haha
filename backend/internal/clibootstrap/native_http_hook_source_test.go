package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/httphook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualNativeHTTPHookSourcePromiseCompletionAndUserConsumer(t *testing.T) {
	profiles := []struct {
		name, body string
		status     int
	}{
		{"empty", "", 204}, {"whitespace", " \t\n\ufeff", 200}, {"success", `{"future":12}`, 200},
		{"block", `{"decision":"block","reason":"actual HTTP blocked \ud800"}`, 200},
		{"prevent", `{"continue":false,"stopReason":"actual prevent"}`, 200},
		{"both", `{"continue":false,"decision":"block","reason":"actual both"}`, 200},
		{"context", `{"systemMessage":"ui-only","hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"actual HTTP context"}}`, 200},
		{"async", `{"async":true,"asyncTimeout":1500}`, 200},
		{"plain", "not JSON", 200}, {"long-plain", strings.Repeat("😀", 150), 200},
		{"malformed", `{"continue":`, 200}, {"invalid", `{"continue":"wrong"}`, 200},
		{"wrong-event", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}`, 200},
		{"status", "HTTP denied", 403},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			var hookInput protocol.JSONObject
			var hookCount, posts atomic.Int32
			hookPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hookCount.Add(1)
				_ = json.NewDecoder(r.Body).Decode(&hookInput)
				if r.Method != "POST" || r.Header.Get("X-Token") != "fake-token" || r.Header.Get("X-Missing") != "" {
					t.Error("physical Hook headers/method")
				}
				w.WriteHeader(profile.status)
				_, _ = io.WriteString(w, profile.body)
			}))
			defer hookPeer.Close()
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				data, _ := io.ReadAll(r.Body)
				if hookCount.Load() != 1 {
					t.Error("model request preceded Hook")
				}
				if bytes.Contains(data, []byte("ui-only")) {
					t.Error("UI system note entered actual API")
				}
				if profile.name == "context" && !bytes.Contains(data, []byte("actual HTTP context")) {
					t.Error("actual API lost Hook context")
				}
				emitPlanFixture(w, 1, "", "HTTP_SOURCE_DONE")
			}))
			defer model.Close()
			hook := map[string]any{"type": "http", "url": hookPeer.URL, "headers": map[string]any{"X-Token": "$HTTP_HOOK_FIXTURE_TOKEN", "X-Missing": "$HTTP_HOOK_FIXTURE_HIDDEN"}, "allowedEnvVars": []string{"HTTP_HOOK_FIXTURE_TOKEN", "HTTP_HOOK_FIXTURE_HIDDEN"}, "statusMessage": "actual HTTP display"}
			m := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
				n.Environment["HTTP_HOOK_FIXTURE_TOKEN"] = "fake\r\n-token"
				n.Environment["HTTP_HOOK_FIXTURE_HIDDEN"] = "must-not-leak"
				settings := map[string]any{"httpHookAllowedEnvVars": []string{"HTTP_HOOK_FIXTURE_TOKEN"}, "hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": []any{hook}}}}}
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) {
				o.Arguments.Tools = []string{"Read,Write"}
				o.Arguments.PermissionMode = "bypassPermissions"
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			inputID, content := "http-original-user", quoteMain("actual original HTTP user")
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(inputID)}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual original HTTP user"})})); err != nil {
				t.Fatal(err)
			}
			defer m.ShutdownSession(context.Background())
			var messages []protocol.JSONObject
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				for _, message := range m.Controller.History() {
					if message.String("type") == "assistant" {
						break
					}
					messages = append(messages, message.Clone())
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if hookCount.Load() != 1 || hookInput.String("session_id") != m.Controller.SessionID() || hookInput.String("hook_event_name") != "UserPromptSubmit" || hookInput.Get("agent_id") != nil {
				t.Fatal("real Hook identity", hookCount.Load(), rawMain(hookInput))
			}
			bun := os.Getenv("CC_HAHA_SOURCE_BUN")
			if bun == "" {
				return
			}
			home := t.TempDir()
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-user-prompt.fixture.ts")
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
			status := profile.status
			row := map[string]any{"base": hookInput, "content": content, "inputUUID": inputID, "permissionMode": "bypassPermissions", "output": map[string]any{"type": "http", "hook": hook, "result": httphook.Result{OK: status >= 200 && status < 300, StatusCode: &status, Body: quoteMain(profile.body)}}}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "rows": []any{row}}))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var source struct {
				Rows []struct {
					Result struct {
						Messages    []protocol.JSONObject
						ShouldQuery bool
					}
				}
			}
			if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != 1 {
				t.Fatal(err, string(output))
			}
			if source.Rows[0].Result.ShouldQuery != (posts.Load() > 0) {
				t.Error("Source shouldQuery", source.Rows[0].Result.ShouldQuery, posts.Load())
			}
			actual, want := rawMain(normalizedUserPromptMessages(messages)), rawMain(normalizedUserPromptMessages(source.Rows[0].Result.Messages))
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
				t.Errorf("actual HTTP Source consumer\nGo%s\nSource%s", actual, want)
			}
		})
	}
}
