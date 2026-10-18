package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
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

func TestTerminalWorkflowPanelActualTaskTreePauseSaveAndDynamicCommandConsumer(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	var parent, child atomic.Int32
	script := `export const meta={name:'terminal-saved-workflow',description:'Actual public panel workflow'};return await agent('PRIVATE_WORKFLOW_AGENT_PROMPT',{label:'ACTUAL_WORKFLOW_CHILD_LABEL'})`
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "You are a subagent spawned by a workflow orchestration script") {
			child.Add(1)
			close(started)
			<-r.Context().Done()
			close(canceled)
			return
		}
		n := parent.Add(1)
		switch n {
		case 1:
			response(w, "workflow-launch", "Workflow", map[string]any{"script": script}, "")
		case 2:
			response(w, "workflow-launch-done", "", nil, "WORKFLOW_PUBLIC_WAITING")
		default:
			if !strings.Contains(string(body), `Run the saved workflow`) {
				t.Error("saved dynamic command was not actual prompt producer", string(body))
			}
			response(w, "workflow-command-next", "", nil, "SAVED_DYNAMIC_COMMAND_NEXT")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.Tools = []string{"Workflow,Read,Write"}
	o.Environment["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Environment["HOME"], "tmp")
	o.Native.Environment["CLAUDE_CODE_TMPDIR"] = o.Environment["CLAUDE_CODE_TMPDIR"]
	_ = os.MkdirAll(o.Environment["CLAUDE_CODE_TMPDIR"], 0700)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = s.Submit(ctx, tui.Input{Text: "Launch the actual workflow"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual worker not started", ctx.Err())
	}
	var panel *tui.LocalPanel
	var notice string
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.Kind == tui.Notice {
			notice = event.Text
		}
		return nil
	}
	open := func() {
		t.Helper()
		if handled, err := s.InvokeTerminalCommand(ctx, "workflows", "", emit); !handled || err != nil {
			t.Fatal(handled, err)
		}
	}
	action := func(name, row string) {
		t.Helper()
		if err := s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: name, Row: row}, emit); err != nil || panel.Notice != "" {
			t.Fatal(name, err, panel.Notice)
		}
	}
	open()
	if len(panel.Rows) != 1 || !strings.Contains(panel.Rows[0].Label, "running") {
		t.Fatal(panel)
	}
	id := panel.Rows[0].ID
	action("workflow-open", id)
	deadline := time.Now().Add(3 * time.Second)
	for len(panel.Rows) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if err = s.InvokeAction(ctx, "app:refreshLocalPanel", emit); err != nil {
			t.Fatal(err)
		}
	}
	if len(panel.Rows) == 0 {
		t.Fatal("actual progress not shown", panel)
	}
	action("workflow-phase", panel.Rows[0].ID)
	if len(panel.Rows) == 0 {
		t.Fatal("actual agent not in phase", panel)
	}
	action("workflow-filter", "")
	if len(panel.Rows) != 1 {
		t.Fatal("running filter dropped actual request", panel)
	}
	if panel.Rows[0].Detail != "PRIVATE_WORKFLOW_AGENT_PROMPT" || !strings.Contains(panel.Rows[0].Label, "ACTUAL_WORKFLOW_CHILD_LABEL") || strings.Contains(panel.Rows[0].Label, "PRIVATE_WORKFLOW_AGENT_PROMPT") {
		t.Fatal("phase did not project the actual promptPreview separately from its label", panel)
	}
	action("workflow-agent", panel.Rows[0].ID)
	if !strings.Contains(panel.Description, "\nPrompt: PRIVATE_WORKFLOW_AGENT_PROMPT\n") || !strings.Contains(panel.Description, "ACTUAL_WORKFLOW_CHILD_LABEL") {
		t.Fatal("prompt detail missing", panel)
	}
	action("workflow-back", "")
	action("workflow-back", "")
	action("workflow-pause", "")
	if !strings.Contains(notice, "resumeFromRunId") || !strings.Contains(notice, "completed agents return cached results") {
		t.Fatal("Source resume result missing", notice)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("pause did not cancel actual worker", ctx.Err())
	}
	var join func(context.Context) error
	if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		task, err := s.process.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return err
		}
		join = task.Join
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = join(ctx); err != nil {
		t.Fatal(err)
	}
	open()
	if !strings.Contains(panel.Rows[0].Label, "paused") {
		t.Fatal("paused same task absent", panel)
	}
	action("workflow-open", id)
	action("workflow-save", "")
	path := filepath.Join(o.WorkDir, ".claude", "workflows", "terminal-saved-workflow.js")
	if data, err := os.ReadFile(path); err != nil || string(data) != script {
		t.Fatal(err, string(data))
	}
	found := false
	commands, _ := s.SubscribeCommands(nil)
	for _, command := range commands {
		found = found || command.Name == "terminal-saved-workflow"
	}
	if !found {
		t.Fatal("reload did not publish dynamic workflow command", commands)
	}
	if parent.Load() != 2 || child.Load() != 1 {
		t.Fatal("local panel requested model", parent.Load(), child.Load())
	}
	if err = s.Submit(ctx, tui.Input{Text: "/terminal-saved-workflow"}, nil); err != nil {
		t.Fatal(err)
	}
	if parent.Load() != 3 {
		t.Fatal("saved command did not use same Harness", parent.Load())
	}
}
