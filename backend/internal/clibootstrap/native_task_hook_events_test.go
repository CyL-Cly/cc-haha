package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func TestActualNativeTaskAndIdleHookWrappersHaveSourceFieldsFramesAndPhysicalProcesses(t *testing.T) {
	for _, event := range []string{"TaskCreated", "TaskCompleted", "TeammateIdle"} {
		for _, profile := range []string{"plain", "optional", "blocked", "json-exit1", "plain-exit2", "plain-exit1", "already-aborted"} {
			t.Run(event+"-"+profile, func(t *testing.T) {
				var journal, command string
				output := map[string]any{"systemMessage": "task-event-system"}
				if profile == "blocked" {
					output["decision"] = "block"
					output["reason"] = "actual-task-hook-block"
					output["continue"] = false
					output["stopReason"] = "actual-stop-reason"
				}
				stdout, stderrText, exitCode := string(rawMain(output))+"\n", "", 0
				if profile == "json-exit1" {
					exitCode = 1
				}
				if profile == "plain-exit2" {
					stdout = ""
					stderrText = "actual-exit2-block"
					exitCode = 2
				}
				if profile == "plain-exit1" {
					stdout = ""
					stderrText = "actual-exit1-error"
					exitCode = 1
				}
				posts := 0
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					posts++
					t.Error("hook actor must not create a second model request")
					http.Error(w, "unexpected model", 500)
				}))
				defer peer.Close()
				m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
					delete(n.Environment, "CLAUDE_CODE_SIMPLE")
					journal = filepath.Join(n.CWD, "task-hook-inputs.jsonl")
					script := filepath.Join(n.CWD, "task-hook.py")
					code := fmt.Sprintf("import sys,json,os\nx=json.load(sys.stdin)\nx['physical_pid']=os.getpid()\nwith open(%q,'a') as f:f.write(json.dumps(x)+'\\n')\nsys.stdout.write(%q)\nsys.stderr.write(%q)\nsys.exit(%d)\n", journal, stdout, stderrText, exitCode)
					if err := os.WriteFile(script, []byte(code), 0600); err != nil {
						t.Fatal(err)
					}
					command = "/usr/bin/python3 " + strconv.Quote(script)
					settings := map[string]any{"hooks": map[string]any{event: []any{map[string]any{"matcher": "ignored-without-matchQuery", "hooks": []any{map[string]any{"type": "command", "command": command}}}}}}
					if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
						t.Fatal(err)
					}
				}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read,Write"} })
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if err := m.StartSession(ctx); err != nil {
					t.Fatal(err)
				}
				defer m.ShutdownSession(context.Background())
				var eventID string
				frames := []protocol.JSONObject{}
				outcomes := []string{}
				var actualBase protocol.JSONObject
				err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					call := queryhooks.Context{Scope: scope, ComputerUseInactive: true, GoalInactive: true, TeammateInactive: true}
					in := queryhooks.NativeTaskHookInput{SessionID: m.Controller.SessionID(), TaskID: quoteMain("task-actual"), TaskSubject: quoteMain("Source actual subject"), Call: call}
					if profile == "optional" || profile == "blocked" {
						in.TaskDescription = quoteMain("")
						in.TeammateName = quoteMain("worker-actual")
						in.TeamName = quoteMain("team-actual")
						in.PermissionMode = quoteMain("acceptEdits")
						in.MainThreadAgentType = quoteMain("actual-main-type")
					}
					runctx := ctx
					if profile == "already-aborted" {
						var cancel context.CancelFunc
						runctx, cancel = context.WithCancel(ctx)
						cancel()
					}
					consume := func(value queryhooks.EventResult) error {
						if value.Outcome != "" && (len(outcomes) == 0 || outcomes[len(outcomes)-1] != value.Outcome) {
							outcomes = append(outcomes, value.Outcome)
						}
						if value.Message != nil {
							if value.Message.Type() == "progress" {
								eventID = value.Message.Record.String("toolUseID")
								return nil
							}
							attachment, _ := value.Message.Record.Object("attachment")
							attachment.Delete("durationMs")
							message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("attachment")}, protocol.JSONField{Name: "attachment", Value: rawMain(attachment)})
							frames = append(frames, protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: rawMain(message)}))
						} else {
							frames = append(frames, value.Fields.Clone())
						}
						return nil
					}
					switch event {
					case "TaskCreated":
						return m.hook.ExecuteTaskCreatedHooks(runctx, in, consume)
					case "TaskCompleted":
						return m.hook.ExecuteTaskCompletedHooks(runctx, in, consume)
					default:
						return m.hook.ExecuteTeammateIdleHooks(runctx, in, consume)
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				if posts != 0 {
					t.Fatal("model quota used by command actor")
				}
				if profile == "already-aborted" {
					if _, err := os.Stat(journal); !os.IsNotExist(err) || len(frames) != 0 {
						t.Fatal("Source pre-aborted event started IO", err, frames)
					}
					return
				}
				data, err := os.ReadFile(journal)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				if len(lines) != 1 {
					t.Fatal("physical Hook process count", len(lines))
				}
				if err = json.Unmarshal([]byte(lines[0]), &actualBase); err != nil {
					t.Fatal(err)
				}
				actualBase.Delete("physical_pid")
				if actualBase.String("session_id") != m.Controller.SessionID() || actualBase.Get("agent_id") != nil || actualBase.Get("stop_hook_active") != nil || eventID == "" {
					t.Fatal("task Hook lost original identity", string(data))
				}
				if profile == "plain" && (actualBase.Get("permission_mode") != nil || actualBase.Get("agent_type") != nil || actualBase.Get("task_description") != nil) {
					t.Fatal("Source undefined optional fields were replaced", string(data))
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
				fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
				base := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: actualBase.Get("session_id")}, protocol.JSONField{Name: "transcript_path", Value: actualBase.Get("transcript_path")}, protocol.JSONField{Name: "cwd", Value: actualBase.Get("cwd")}, protocol.JSONField{Name: "agent_type", Value: actualBase.Get("agent_type")})
				blocking := protocol.NewJSONObject(protocol.JSONField{Name: "blockingError", Value: json.RawMessage(`"reason \ud800"`)}, protocol.JSONField{Name: "command", Value: quoteMain(command)})
				row := protocol.NewJSONObject(protocol.JSONField{Name: "event", Value: quoteMain(event)}, protocol.JSONField{Name: "eventID", Value: quoteMain(eventID)}, protocol.JSONField{Name: "base", Value: rawMain(base)}, protocol.JSONField{Name: "taskID", Value: quoteMain("task-actual")}, protocol.JSONField{Name: "taskSubject", Value: quoteMain("Source actual subject")}, protocol.JSONField{Name: "taskDescription", Value: actualBase.Get("task_description")}, protocol.JSONField{Name: "teammateName", Value: actualBase.Get("teammate_name")}, protocol.JSONField{Name: "teamName", Value: actualBase.Get("team_name")}, protocol.JSONField{Name: "permissionMode", Value: actualBase.Get("permission_mode")}, protocol.JSONField{Name: "output", Value: rawMain(map[string]any{"command": command, "stdout": stdout, "stderr": stderrText, "code": exitCode})}, protocol.JSONField{Name: "blockingError", Value: rawMain(blocking)})
				cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				cmd.Dir = home
				cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
				cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "taskEventWrappers": []protocol.JSONObject{row}}))
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil {
					t.Fatal(err, stderr.String())
				}
				var source struct {
					Rows []struct {
						Input    protocol.JSONObject
						Frames   []protocol.JSONObject
						Feedback json.RawMessage
						Outcomes []string
					}
				}
				if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != 1 {
					t.Fatal(err, string(out))
				}
				if actual, want := rawMain(actualBase), rawMain(source.Rows[0].Input); !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
					t.Errorf("Source task input Go%s Source%s", actual, want)
				}
				if actual, want := rawMain(frames), rawMain(source.Rows[0].Frames); !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
					t.Errorf("Source task frames Go%s Source%s", actual, want)
				}
				if actual, want := rawMain(outcomes), rawMain(source.Rows[0].Outcomes); !bytes.Equal(actual, want) {
					t.Errorf("Source command completion outcome Go%s Source%s", actual, want)
				}
				feedback, err := queryhooks.FormatTaskHookFeedback(event, blocking)
				if err != nil || !bytes.Equal(feedback, source.Rows[0].Feedback) {
					t.Errorf("Source task feedback Go%s Source%s %v", feedback, source.Rows[0].Feedback, err)
				}
			})
		}
	}
}
