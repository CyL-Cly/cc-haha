package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestForegroundREPLReturnsWithPhysicalBackgroundTaskWhileHeadlessAndBlockingOutputWait(t *testing.T) {
	for _, scenario := range []string{"interactive", "headless", "blocking-output"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			var command, taskID string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				ordinal := calls.Add(1)
				if ordinal == 1 {
					response(w, "foreground-background", "Bash", map[string]any{"command": command, "description": "actual detached terminal work", "run_in_background": true}, "")
					return
				}
				if ordinal == 2 && scenario == "blocking-output" {
					text := string(data)
					at := strings.Index(text, "Command running in background with ID: ")
					if at < 0 {
						t.Error("actual task ID missing")
						response(w, "bad", "", nil, "bad")
						return
					}
					tail := text[at+len("Command running in background with ID: "):]
					taskID = strings.Fields(tail)[0]
					taskID = strings.Trim(taskID, ".\\\"\n")
					response(w, "foreground-output", "TaskOutput", map[string]any{"task_id": taskID, "block": true, "timeout": 5000}, "")
					return
				}
				response(w, "foreground-complete", "", nil, "FOREGROUND_COMPLETED")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Interactive = scenario != "headless"
			o.Arguments.Tools = []string{"Bash,TaskOutput,TaskStop"}
			o.Native.Environment["CLAUDE_CODE_TMPDIR"] = o.Native.Temp
			started, release, completed := filepath.Join(o.WorkDir, "started"), filepath.Join(o.WorkDir, "release"), filepath.Join(o.WorkDir, "completed")
			command = "printf STARTED > " + started + "; while [ ! -e " + release + " ]; do sleep 0.05; done; printf PHYSICAL_FINISHED > " + completed
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- s.Submit(ctx, tui.Input{Text: "Start detached work", Mode: "prompt"}, nil) }()
			deadline := time.Now().Add(4 * time.Second)
			for {
				if _, err = os.Stat(started); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("physical shell did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if scenario == "interactive" {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("foreground waited for detached work")
				}
				value, err := s.process.TerminalPanelSnapshot(ctx, "tasks")
				if err != nil {
					t.Fatal(err)
				}
				var rows []protocol.JSONObject
				_ = json.Unmarshal(value.Get("tasks"), &rows)
				if len(rows) != 1 || rows[0].String("status") != "running" {
					t.Fatal("pending task lost before menu", value)
				}
				var panel *tui.LocalPanel
				emit := func(event tui.Event) error { panel = event.LocalPanel; return nil }
				if _, err = s.InvokeTerminalCommand(ctx, "tasks", "", emit); err != nil {
					t.Fatal(err)
				}
				if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "stop", Row: panel.Rows[0].ID}, emit); err != nil {
					t.Fatal(err)
				}
				var join func(context.Context) error
				if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					task, err := s.process.Controller.BackgroundTaskOnOwner(scope, rows[0].String("id"))
					if err != nil {
						return err
					}
					if task == nil || task.Data.String("status") != "killed" {
						t.Error("stop did not settle same task")
					}
					join = task.Join
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err = join(ctx); err != nil {
					t.Fatal("stopped physical work not joined", err)
				}
				if _, err = os.Stat(completed); !os.IsNotExist(err) {
					t.Fatal("stopped shell finished side effect", err)
				}
			} else {
				select {
				case err := <-done:
					t.Fatal("wait boundary returned before detached completion", err)
				case <-time.After(150 * time.Millisecond):
				}
				if err = os.WriteFile(release, []byte("release"), 0600); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("wait did not finish", ctx.Err())
				}
				data, err := os.ReadFile(completed)
				if err != nil || string(data) != "PHYSICAL_FINISHED" {
					t.Fatal(err, string(data))
				}
			}
		})
	}
}
