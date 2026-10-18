package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalWorkflowControlsCancelActualWorkerAndKeepTaskOwnedState(t *testing.T) {
	for _, action := range []string{"pause", "skip", "retry"} {
		t.Run(action, func(t *testing.T) {
			started, canceled := make(chan struct{}), make(chan struct{})
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(started)
				<-r.Context().Done()
				close(canceled)
			}))
			defer peer.Close()
			m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") })
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var launched protocol.JSONObject
			script := `export const meta={name:'terminal-control',description:'Actual terminal workflow control'};return await agent('Wait for physical terminal cancellation')`
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				launched, err = m.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(script)}), quoteMain("terminal-workflow-tool"))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("actual worker never opened", ctx.Err())
			}
			key := launched.String("runId") + "-1"
			if _, err := m.TerminalWorkflowControl(ctx, launched.String("taskId"), action, key); err != nil {
				t.Fatal(err)
			}
			select {
			case <-canceled:
			case <-ctx.Done():
				t.Fatal("actual request not canceled", ctx.Err())
			}
			var row protocol.JSONObject
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				task, err := m.Controller.BackgroundTaskOnOwner(scope, launched.String("taskId"))
				if err != nil {
					return err
				}
				if task.WorkflowControl == nil {
					t.Fatal("lookup lost actual control closure")
				}
				future, err := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, task.Join(ctx) })
				if err != nil {
					return err
				}
				if _, err = future.Await(scope); err != nil {
					return err
				}
				task, err = m.Controller.BackgroundTaskOnOwner(scope, launched.String("taskId"))
				if err == nil {
					row = task.Data.Clone()
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{"pause": "paused", "skip": "completed", "retry": "failed"}[action]
			if row.String("status") != expected {
				t.Fatal(action, string(rawMain(row)))
			}
			if action == "skip" && !strings.Contains(string(row.Get("workflowProgress")), "skipped by user") {
				t.Fatal("skip not consumed by original agent runtime", string(rawMain(row)))
			}
			if action == "pause" {
				if string(row.Get("notified")) != "true" || row.Get("evictAfter") != nil {
					t.Fatal("paused Source lifecycle changed", string(rawMain(row)))
				}
				saved, err := m.SaveTerminalWorkflow(ctx, launched.String("taskId"), "project")
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(saved.String("filePath"))
				if err != nil || string(data) != script {
					t.Fatal("actual script not saved", err, string(data))
				}
				if filepath.Base(saved.String("filePath")) != "terminal-control.js" {
					t.Fatal(saved)
				}
			}
			if _, err := m.TerminalWorkflowControl(ctx, launched.String("taskId"), action, key); err == nil {
				t.Fatal("terminal task reused active callback")
			}
			if _, err := m.TerminalWorkflowControl(ctx, "missing", "pause", ""); err == nil {
				t.Fatal("missing task admitted")
			}
		})
	}
}

func TestTerminalWorkflowSourceGateTracksSameSettingsAndSaveRejectsNonWorkflow(t *testing.T) {
	m := taskMainFixture(t)
	if !m.NativeWorkflowsCommandEnabled() {
		t.Fatal("default Source workflow gate off")
	}
	if err := m.SetTerminalConfigSetting(context.Background(), "enableWorkflows", []byte("false")); err != nil {
		t.Fatal(err)
	}
	if m.NativeWorkflowsCommandEnabled() {
		t.Fatal("actual user settings ignored")
	}
	if err := m.SetTerminalConfigSetting(context.Background(), "enableWorkflows", []byte("true")); err != nil {
		t.Fatal(err)
	}
	if !m.NativeWorkflowsCommandEnabled() {
		t.Fatal("actual gate did not re-enable")
	}
	if _, err := m.SaveTerminalWorkflow(context.Background(), "not-a-run", "project"); err == nil {
		t.Fatal("unbound saved fake workflow")
	}
}
