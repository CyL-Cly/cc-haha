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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func TestActualNativeTaskPromptContextAbsenceMatchesOriginalWrapperAndCreatesNoModel(t *testing.T) {
	for _, event := range []string{"TaskCreated", "TaskCompleted", "TeammateIdle"} {
		t.Run(event, func(t *testing.T) {
			count := atomic.Int32{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				t.Error("missing original ToolUseContext created a model request")
				http.Error(w, "unexpected", 500)
			}))
			defer peer.Close()
			hook := map[string]any{"type": "prompt", "prompt": "Source missing context $ARGUMENTS", "model": "fixture-model", "statusMessage": "actual missing context"}
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(map[string]any{"hooks": map[string]any{event: []any{map[string]any{"hooks": []any{hook}}}}}), 0600); err != nil {
					t.Fatal(err)
				}
			}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read,Write"} })
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			defer m.ShutdownSession(context.Background())
			frames := []protocol.JSONObject{}
			var eventID string
			var base protocol.JSONObject
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				call := queryhooks.Context{Scope: scope}
				if event == "TeammateIdle" {
					call.ToolUseContext = m.Host
				}
				in := queryhooks.NativeTaskHookInput{SessionID: m.Controller.SessionID(), TaskID: quoteMain("actual-task-id"), TaskSubject: quoteMain("actual task subject"), TeammateName: quoteMain("actual teammate"), TeamName: quoteMain("actual team"), Call: call}
				consume := func(value queryhooks.EventResult) error {
					if value.Message != nil {
						if value.Message.Type() == "progress" {
							eventID = value.Message.Record.String("toolUseID")
							return nil
						}
						attachment, _ := value.Message.Record.Object("attachment")
						attachment.Delete("durationMs")
						frames = append(frames, protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("attachment")}, protocol.JSONField{Name: "attachment", Value: rawMain(attachment)}))}))
					} else {
						frames = append(frames, value.Fields.Clone())
					}
					return nil
				}
				path, err := m.Controller.Writer().ComputedTranscriptPath(scope)
				if err != nil {
					return err
				}
				base = protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "transcript_path", Value: quoteMain(path)}, protocol.JSONField{Name: "cwd", Value: quoteMain(m.state.Snapshot().CWD)})
				switch event {
				case "TaskCreated":
					return m.hook.ExecuteTaskCreatedHooks(ctx, in, consume)
				case "TaskCompleted":
					return m.hook.ExecuteTaskCompletedHooks(ctx, in, consume)
				default:
					return m.hook.ExecuteTeammateIdleHooks(ctx, in, consume)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if count.Load() != 0 || len(frames) != 1 || eventID == "" {
				t.Fatal("original absence was not retained", count.Load(), string(rawMain(frames)))
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
			row := map[string]any{"event": event, "eventID": eventID, "base": base, "taskID": "actual-task-id", "taskSubject": "actual task subject", "teammateName": "actual teammate", "teamName": "actual team", "blockingError": map[string]any{"blockingError": "unused", "command": "unused"}, "output": map[string]any{"type": "prompt", "hook": hook}}
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "taskEventWrappers": []any{row}}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err, stderr.String())
			}
			var source struct {
				Rows []struct{ Frames []protocol.JSONObject }
			}
			if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != 1 {
				t.Fatal(err, string(out))
			}
			actual, want := rawMain(frames), rawMain(source.Rows[0].Frames)
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
				t.Errorf("Source missing-context completion differs\nGo%s\nSource%s", actual, want)
			}
		})
	}
}
