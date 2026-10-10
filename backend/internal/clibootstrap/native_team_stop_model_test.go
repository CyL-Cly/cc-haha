package clibootstrap

import (
	"context"
	"errors"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func TestActualMainWorkerIdleVetoContinuesSameModelAndNormalStopRunsFirst(t *testing.T) {
	for _, event := range []string{"Stop", "TaskCompleted", "TeammateIdle"} {
		t.Run(event, func(t *testing.T) {
			var posts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				round := int(posts.Add(1))
				if round == 2 && !strings.Contains(string(body), event+" hook feedback:") {
					t.Errorf("actual model did not consume veto %s", body)
				}
				if strings.Contains(string(body), `"hook_progress"`) {
					t.Error("bookkeeping hook progress entered physical model request")
				}
				emitWorktreeModel(w, round, "", "", "Actual worker final")
			}))
			defer peer.Close()
			names := []string{"Read"}
			m, f, _ := teamWorkerFixture(t, teamworker.Start{ParentSessionID: "parent", TeamName: "actual-team", MemberID: "m", Name: "worker", SystemPrompt: "Actual MAIN stop hook model", Tools: &names, AgentDefinition: protocol.NewJSONObject()}, peer.URL)
			delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
			sentinel := filepath.Join(f.cwd, "veto-once")
			input := filepath.Join(f.cwd, "actual-stop-input.json")
			idle := filepath.Join(f.cwd, "idle-after-task.json")
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
			command := "cat > " + quote(input) + "; if [ ! -f " + quote(sentinel) + " ]; then touch " + quote(sentinel) + "; echo ACTUAL_RETRY_REQUIRED >&2; exit 2; fi"
			if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				hooks := protocol.NewJSONObject(protocol.JSONField{Name: event, Value: rawMain([]any{map[string]any{"hooks": []any{map[string]string{"type": "command", "command": command}}}})})
				if event == "TaskCompleted" || event == "Stop" {
					idleCommand := "cat > " + quote(idle)
					if event == "Stop" {
						idleCommand += "; test -f " + quote(sentinel) + " || { echo NORMAL_STOP_NOT_FINISHED >&2; exit 2; }"
					}
					hooks.Set("TeammateIdle", rawMain([]any{map[string]any{"hooks": []any{map[string]string{"type": "command", "command": idleCommand}}}}))
				}
				m.teamWorker.preset.Definition.Fields.Set("hooks", rawMain(hooks))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := m.StartSession(ctx); err != nil {
				t.Fatal(err)
			}
			if event == "TaskCompleted" {
				if _, err := m.taskStore.Create("actual-team", objMain([]byte(`{"subject":"owned work","description":"actual","status":"in_progress","owner":"worker","blocks":[],"blockedBy":[]}`))); err != nil {
					t.Fatal(err)
				}
			}
			user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Finish the actual approved worker task"})})
			if err := m.RunTurn(ctx, user); err != nil {
				t.Fatal(err)
			}
			if posts.Load() != 2 {
				t.Fatalf("veto did not continue exactly once: %d", posts.Load())
			}
			data, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			v := objMain(data)
			if v.String("hook_event_name") != event || v.String("permission_mode") != "bypassPermissions" || event != "Stop" && v.String("team_name") != "actual-team" || v.Get("agent_id") != nil {
				t.Fatalf("actual hook input %s", data)
			}
			if event == "TaskCompleted" || event == "Stop" {
				if _, err := os.Stat(idle); err != nil {
					t.Fatal("idle did not run after task hook", err)
				}
			}
			var transcript string
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				retained := 0
				for _, record := range m.Controller.History() {
					data := objMain(record.Get("data"))
					if record.String("type") == "progress" && data.String("type") == "hook_progress" && data.String("hookEvent") == event {
						retained++
						if record.String("toolUseID") == "" || record.String("parentToolUseID") != record.String("toolUseID") {
							return errors.New("live hook progress lost actual child identity")
						}
					}
				}
				if retained != 2 {
					return fmt.Errorf("mutableMessages lost actual hook progress: %d", retained)
				}
				var err error
				transcript, err = m.Controller.Writer().ComputedTranscriptPath(scope)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := m.ShutdownSession(context.Background()); err != nil {
				t.Fatal(err)
			}
			physical, err := os.ReadFile(transcript)
			if err != nil {
				t.Fatal(err)
			}
			progress := 0
			for _, line := range strings.Split(string(physical), "\n") {
				record := objMain([]byte(line))
				data := objMain(record.Get("data"))
				if record.String("type") == "progress" && data.String("type") == "hook_progress" && data.String("hookEvent") == event {
					progress++
					if record.String("toolUseID") == "" || record.String("toolUseID") != record.String("parentToolUseID") {
						t.Fatalf("wrong real hook lineage %s", line)
					}
				}
			}
			if progress != 0 {
				t.Fatalf("Source isLoggableMessage filters progress; raw JSONL retained %d", progress)
			}
		})
	}
}

func TestActualMainWorkerIdleCancellationDrainsSameNativeProcess(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		emitWorktreeModel(w, int(posts.Add(1)), "", "", "Actual worker final before idle")
	}))
	defer peer.Close()
	names := []string{"Read"}
	m, f, _ := teamWorkerFixture(t, teamworker.Start{ParentSessionID: "parent", TeamName: "actual-team", MemberID: "m", Name: "worker", SystemPrompt: "Actual idle cancellation", Tools: &names, AgentDefinition: protocol.NewJSONObject()}, peer.URL)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	path := filepath.Join(f.cwd, "actual-idle-process-started.json")
	command := "cat > '" + strings.ReplaceAll(path, "'", "'\\''") + "'; sleep 30"
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		m.teamWorker.preset.Definition.Fields.Set("hooks", rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "TeammateIdle", Value: rawMain([]any{map[string]any{"hooks": []any{map[string]string{"type": "command", "command": command}}}})})))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Execute actual idle cancellation"})})
	done := make(chan error, 1)
	go func() { done <- m.RunTurn(ctx, user) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("actual idle command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		// Source handleStopHooks returns preventContinuation on abort;
		// that is a normal completed iterator result, not a thrown error.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("actual caller cancellation lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actual idle command did not drain on caller cancellation")
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if err := m.ShutdownSession(closeCtx); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("cancel sampled another model: %d", posts.Load())
	}
}
