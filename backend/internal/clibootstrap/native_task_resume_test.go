package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualTaskOutputDirectoryCapturesActivatedResumeForkSIDAndRemainsMemoized(t *testing.T) {
	const legacySID = "777356f4-e5f6-4b45-85bb-23c48db5b4cf"
	const clearedSID = "999356f4-e5f6-4b45-85bb-23c48db5b4cf"
	for _, scenario := range []string{"fresh", "resume", "fork"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "private-task-resume-fake-key"
			f.n.Environment["PATH"] = "/usr/bin:/bin"
			f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
			f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
			if e := os.MkdirAll(f.n.Temp, 0700); e != nil {
				t.Fatal(e)
			}
			// NewProcessNativeBoundary captures the real cwd, as the public
			// CLI does; seed the canonical project tree used by its SID loader.
			f.cwd = f.n.CWD
			rows := []map[string]any{
				{"type": "user", "uuid": "10000000-0000-4000-8000-000000000001", "timestamp": "2023-11-14T22:13:20.000Z", "cwd": f.cwd, "sessionId": legacySID, "parentUuid": nil, "message": map[string]any{"role": "user", "content": "Original persisted request"}},
				{"type": "assistant", "uuid": "10000000-0000-4000-8000-000000000002", "timestamp": "2023-11-14T22:13:20.001Z", "cwd": f.cwd, "sessionId": legacySID, "parentUuid": "10000000-0000-4000-8000-000000000001", "message": map[string]any{"role": "assistant", "model": "fixture-model", "content": []any{map[string]any{"type": "text", "text": "Original persisted answer"}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}, "stop_reason": "end_turn"}},
			}
			var transcript bytes.Buffer
			for _, row := range rows {
				data, e := json.Marshal(row)
				if e != nil {
					t.Fatal(e)
				}
				transcript.Write(data)
				transcript.WriteByte('\n')
			}
			f.write(t, filepath.Join(f.config, "projects", projectpath.Sanitize(f.cwd), legacySID+".jsonl"), transcript.String())
			disabled := false
			arguments := PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "No model needed for physical resume task path", Tools: []string{"Bash,TaskOutput,TaskStop"}, MaxTurns: 4, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}
			if scenario != "fresh" {
				arguments.Resume = legacySID
			}
			arguments.ForkSession = scenario == "fork"
			m, e := NewMainPrint(MainPrintOptions{Arguments: arguments, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			startupSID := m.Controller.SessionID()
			if m.taskOutputDirectory != "" {
				t.Fatal("constructor eagerly captured startup SID")
			}
			if e = m.StartSession(context.Background()); e != nil {
				t.Fatal(e)
			}
			activeSID := m.Controller.SessionID()
			if scenario == "resume" && (activeSID != legacySID || activeSID == startupSID) {
				t.Fatal("actual resume did not adopt original SID", activeSID, startupSID)
			}
			if scenario == "fork" && activeSID == legacySID {
				t.Fatal("fork reused old SID")
			}
			if m.taskOutputDirectory != "" {
				t.Fatal("host initialization eagerly captured task directory")
			}
			started, mapped := taskToolCall(t, m, "Bash", `{"command":"printf actual-resume-task-marker","run_in_background":true}`)
			id := started.String("backgroundTaskId")
			result, _ := taskToolCall(t, m, "TaskOutput", `{"task_id":"`+id+`","block":true,"timeout":5000}`)
			if result.String("retrieval_status") != "success" || taskObject(t, result.Get("task")).String("output") != "actual-resume-task-marker" {
				t.Fatal(result)
			}
			directory := m.taskOutputDirectory
			if filepath.Base(filepath.Dir(directory)) != activeSID || !strings.Contains(mapped.String("content"), filepath.Join(directory, id+".output")) {
				t.Fatal("task output used pre-activation SID", activeSID, directory, mapped)
			}
			data, e := os.ReadFile(filepath.Join(directory, id+".output"))
			if e != nil || string(data) != "actual-resume-task-marker" {
				t.Fatal(string(data), e)
			}
			if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if e := m.state.SwitchSession(scope, clearedSID, nil); e != nil {
					return e
				}
				got, e := m.nativeTaskOutputDirectory(scope)
				if got != directory {
					t.Fatal("session change abandoned surviving task", got, directory)
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if bun := os.Getenv("CC_HAHA_SOURCE_BUN"); bun != "" {
				fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-background-tasks.fixture.ts")
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
				cmd.Dir = f.n.Home
				cmd.Env = []string{"HOME=" + f.n.Home, "USERPROFILE=" + f.n.Home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CLAUDE_CODE_TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.n.Home), "CC_HAHA_NATIVE_BACKGROUND_TASK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
				payload, _ := json.Marshal(map[string]any{"homeDir": f.n.Home, "cwd": f.cwd, "startupSessionId": startupSID, "sessionId": activeSID, "diskSessionSequence": []string{clearedSID}})
				cmd.Stdin = bytes.NewReader(payload)
				out, e := cmd.CombinedOutput()
				if e != nil {
					t.Fatalf("Source %v %.1600s", e, out)
				}
				var source struct {
					DirectorySequence []string
					Capabilities      struct{ Blocked []string }
				}
				if e = json.Unmarshal(out, &source); e != nil || len(source.DirectorySequence) != 2 || len(source.Capabilities.Blocked) > 0 {
					t.Fatalf("Source shape %v %.1600s", e, out)
				}
				for _, got := range source.DirectorySequence {
					if got != directory {
						t.Fatal("Source first-use memoization differs", got, directory)
					}
				}
			}
		})
	}
}
