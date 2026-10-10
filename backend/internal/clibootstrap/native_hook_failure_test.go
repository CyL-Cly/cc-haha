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

func TestActualNativePostToolFailureHookRunsOriginalFacadeAndNextModelAfterPhysicalShellError(t *testing.T) {
	for _, profile := range []string{"context", "blocking", "skip"} {
		t.Run(profile, func(t *testing.T) {
			var journal, target, hookCommand string
			posts := atomic.Int32{}
			var last []byte
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				index := posts.Add(1)
				last = body
				if index == 1 {
					emitNativeTaskTool(w, "actual-failed-bash", "Bash", map[string]any{"command": "printf actual-shell-error >&2; exit 7"})
					return
				}
				if index == 2 {
					if !strings.Contains(string(body), "actual-shell-error") || !strings.Contains(string(body), `"is_error":true`) {
						t.Errorf("actual failed Bash feedback absent %s", body)
					}
					if profile != "skip" && !strings.Contains(string(body), "post-failure-extra") {
						t.Errorf("actual failure context missing %s", body)
					}
					if strings.Contains(string(body), "ui-only-failure-message") {
						t.Error("Failure facade ignored systemMessage must not enter API")
					}
					emitNativeTaskTool(w, "after-failure-write", "Write", map[string]any{"file_path": target, "content": "continued-after-physical-failure"})
					return
				}
				if index == 3 {
					emitNativeTaskTool(w, "after-failure-read", "Read", map[string]any{"file_path": target})
					return
				}
				if !strings.Contains(string(body), "continued-after-physical-failure") {
					t.Errorf("physical Read after failure missing %s", body)
				}
				emitPlanFixture(w, int(index), "", "FAILURE_HOOK_DONE")
			}))
			defer peer.Close()
			output := map[string]any{"systemMessage": "ui-only-failure-message", "hookSpecificOutput": map[string]any{"hookEventName": "PostToolUseFailure", "additionalContext": "post-failure-extra"}}
			if profile == "blocking" {
				output["decision"] = "block"
				output["reason"] = "failure-hook-block-reason"
				output["continue"] = false
				output["stopReason"] = "ignored-failure-continuation"
			}
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				target = filepath.Join(n.CWD, "after-failure.txt")
				journal = filepath.Join(n.CWD, "failure-inputs.jsonl")
				script := filepath.Join(n.CWD, "failure-hook.py")
				code := fmt.Sprintf("import sys,json,os\nx=json.load(sys.stdin)\nx['physical_pid']=os.getpid()\nwith open(%q,'a') as f:f.write(json.dumps(x)+'\\n')\nprint(%q)\n", journal, string(rawMain(output)))
				if err := os.WriteFile(script, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				hookCommand = "/usr/bin/python3 " + strconv.Quote(script)
				condition := "Bash"
				if profile == "skip" {
					condition = "Read"
				}
				settings := map[string]any{"hooks": map[string]any{"PostToolUseFailure": []any{map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": hookCommand, "if": condition}}}}}}
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) {
				o.Arguments.Tools = []string{"Bash,Write,Read"}
				o.Arguments.PermissionMode = "bypassPermissions"
			})
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "actual failure hook"})})); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "continued-after-physical-failure" {
				t.Fatalf("actual file after failure %q %v", data, err)
			}
			if posts.Load() != 4 || len(last) == 0 {
				t.Fatal("same actual Harness did not continue", posts.Load())
			}
			attachments := []protocol.JSONObject{}
			receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
				for _, message := range m.Controller.History() {
					a, ok := message.Object("attachment")
					if ok && a.String("hookEvent") == "PostToolUseFailure" {
						attachments = append(attachments, a.Clone())
					}
				}
				return nil
			})
			if err == nil {
				err = receipt.Wait()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
			if profile == "skip" {
				if _, err = os.Stat(journal); !os.IsNotExist(err) || len(attachments) != 0 {
					t.Fatal("unmatched Failure if ran process", err, attachments)
				}
				return
			}
			data, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 1 {
				t.Fatal("actual failure hook process count", len(lines))
			}
			var input protocol.JSONObject
			if err = json.Unmarshal([]byte(lines[0]), &input); err != nil {
				t.Fatal(err)
			}
			if input.String("hook_event_name") != "PostToolUseFailure" || input.String("tool_name") != "Bash" || input.String("session_id") != m.Controller.SessionID() || input.String("tool_use_id") != "actual-failed-bash" || string(input.Get("is_interrupt")) != "false" || !strings.Contains(input.String("error"), "actual-shell-error") || input.Get("tool_response") != nil {
				t.Fatal("physical stdin mismatch", string(data))
			}
			if bun := os.Getenv("CC_HAHA_SOURCE_BUN"); bun != "" {
				home := filepath.Dir(journal)
				fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
				row := map[string]any{"toolName": "Bash", "toolUseID": "actual-failed-bash", "input": map[string]any{"command": "printf actual-shell-error >&2; exit 7"}, "error": input.Get("error"), "isInterrupt": false, "executeFailure": true, "outputs": map[string]any{"PostToolUseFailure": []any{map[string]any{"command": hookCommand, "stdout": string(rawMain(output)) + "\n"}}}}
				payload := rawMain(map[string]any{"homeDir": home, "sessionID": m.Controller.SessionID(), "toolFacades": []any{row}})
				cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				cmd.Dir = home
				cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
				cmd.Stdin = bytes.NewReader(payload)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil {
					t.Fatal(err, stderr.String())
				}
				var source struct {
					Observations []struct{ Attachments []protocol.JSONObject }
				}
				if err = json.Unmarshal(out, &source); err != nil || len(source.Observations) != 1 {
					t.Fatal(err, string(out))
				}
				for i := range attachments {
					attachments[i].Delete("durationMs")
				}
				actual, want := rawMain(attachments), rawMain(source.Observations[0].Attachments)
				if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
					t.Errorf("actual Source Failure facade differs\nGo%s\nSource%s", actual, want)
				}
			}
		})
	}
}
