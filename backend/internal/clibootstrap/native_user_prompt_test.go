package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func normalizedUserPromptMessages(values []protocol.JSONObject) []protocol.JSONObject {
	result := []protocol.JSONObject{}
	for _, value := range values {
		copy := value.Clone()
		copy.Delete("uuid")
		copy.Delete("timestamp")
		if attachment, ok := copy.Object("attachment"); ok {
			attachment.Delete("toolUseID")
			attachment.Delete("durationMs")
			copy.Set("attachment", rawMain(attachment))
		}
		result = append(result, copy)
	}
	return result
}

func TestActualNativeUserPromptSubmitSourceConsumerAndPhysicalCommandBeforeModel(t *testing.T) {
	for _, profile := range []string{"plain", "empty", "context", "long-context", "blocked", "prevent", "block-and-prevent", "exit1", "array-blocked"} {
		t.Run(profile, func(t *testing.T) {
			content := quoteMain("original actual user prompt")
			output := "plain actual hook context\n"
			stderrText, exitCode := "", 0
			decision := protocol.NewJSONObject()
			if profile == "empty" {
				output = ""
			}
			if profile == "context" || profile == "long-context" {
				text := quoteMain("actual additional context")
				if profile == "long-context" {
					text = []byte(`"` + strings.Repeat("a", 9999) + `\ud800\udc00tail"`)
				}
				decision.Set("systemMessage", quoteMain("actual ui-only note"))
				decision.Set("hookSpecificOutput", rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "hookEventName", Value: quoteMain("UserPromptSubmit")}, protocol.JSONField{Name: "additionalContext", Value: text})))
			}
			blocked := profile == "blocked" || profile == "array-blocked" || profile == "block-and-prevent"
			prevented := profile == "prevent" || profile == "block-and-prevent"
			if blocked {
				decision.Set("decision", quoteMain("block"))
				decision.Set("reason", []byte(`"actual block \ud800"`))
			}
			if prevented {
				decision.Set("continue", []byte("false"))
				decision.Set("stopReason", quoteMain("actual prevented reason"))
			}
			if profile == "array-blocked" {
				content = rawMain([]any{map[string]any{"type": "text", "text": "first original"}, map[string]any{"type": "text", "text": " last original \u00a0"}})
			}
			if profile == "exit1" {
				output, stderrText, exitCode = "", "physical prompt nonblocking error", 1
			}
			if len(decision.OwnProperties()) > 0 {
				output = string(rawMain(decision)) + "\n"
			}
			var journal, command string
			posts := atomic.Int32{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				posts.Add(1)
				if _, err := os.Stat(journal); err != nil {
					t.Error("model POST arrived before original Hook command completed", err)
				}
				body, _ := io.ReadAll(request.Body)
				if strings.Contains(string(body), "actual ui-only note") {
					t.Error("UI-only Hook system message entered API")
				}
				if profile == "context" && !bytes.Contains(body, []byte("actual additional context")) {
					t.Error("Hook context missing from actual model POST")
				}
				emitPlanFixture(w, 1, "", "USER_PROMPT_HOOK_DONE")
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
				journal = filepath.Join(n.CWD, "user-prompt-input.json")
				script := filepath.Join(n.CWD, "user-prompt-hook.py")
				code := fmt.Sprintf("import os,json,sys\nx=json.load(sys.stdin)\nx['physical_pid']=os.getpid()\nwith open(%q,'w') as f:json.dump(x,f)\nsys.stdout.write(%q)\nsys.stderr.write(%q)\nsys.exit(%d)\n", journal, output, stderrText, exitCode)
				if err := os.WriteFile(script, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				command = "/usr/bin/python3 " + strconv.Quote(script)
				settings := map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"matcher": "unused-no-matchQuery", "hooks": []any{map[string]any{"type": "command", "command": command}}}}}}
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) {
				o.Arguments.Tools = []string{"Read,Write"}
				o.Arguments.PermissionMode = "bypassPermissions"
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			inputID := "original-prompt-input"
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(inputID)}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: content}))})); err != nil {
				t.Fatal(err)
			}
			defer m.ShutdownSession(context.Background())
			if got, want := posts.Load(), int32(1); got != want && !(got == 0 && (blocked || prevented)) {
				t.Fatal("actual shouldQuery branch", got, blocked, prevented)
			}
			if (blocked || prevented) && posts.Load() != 0 {
				t.Fatal("blocked input consumed model quota")
			}
			var base protocol.JSONObject
			if data, err := os.ReadFile(journal); err != nil || json.Unmarshal(data, &base) != nil {
				t.Fatal("physical Hook stdin evidence", err)
			}
			if base.String("session_id") != m.Controller.SessionID() || base.String("hook_event_name") != "UserPromptSubmit" || base.Get("agent_id") != nil {
				t.Fatal("original wrapper identity", string(rawMain(base)))
			}
			base.Delete("physical_pid")
			var initial []protocol.JSONObject
			receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
				for _, value := range m.Controller.History() {
					if value.String("type") == "assistant" {
						break
					}
					initial = append(initial, value.Clone())
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = receipt.Wait(); err != nil {
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
			row := protocol.NewJSONObject(protocol.JSONField{Name: "base", Value: rawMain(base)}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "inputUUID", Value: quoteMain(inputID)}, protocol.JSONField{Name: "permissionMode", Value: quoteMain("bypassPermissions")}, protocol.JSONField{Name: "output", Value: rawMain(map[string]any{"command": command, "stdout": output, "stderr": stderrText, "code": exitCode})})
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
					Input  protocol.JSONObject
					Result struct {
						Messages    []protocol.JSONObject
						ShouldQuery bool
					}
				}
			}
			if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != 1 {
				t.Fatal(err, string(out))
			}
			if actual, want := rawMain(base), rawMain(source.Rows[0].Input); !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
				t.Errorf("Source prompt stdin Go%s Source%s", actual, want)
			}
			if source.Rows[0].Result.ShouldQuery != (posts.Load() > 0) {
				t.Error("original shouldQuery", source.Rows[0].Result.ShouldQuery, posts.Load())
			}
			if actual, want := rawMain(normalizedUserPromptMessages(initial)), rawMain(normalizedUserPromptMessages(source.Rows[0].Result.Messages)); !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
				t.Errorf("Source original input consumer Go%s Source%s", actual, want)
			}
		})
	}
}
