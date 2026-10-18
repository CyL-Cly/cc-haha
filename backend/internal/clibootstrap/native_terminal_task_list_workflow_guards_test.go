package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestNativeWorkflowTaskViewRejectsForeignRetiredScopeAndClosedParent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Error("Task callback scope guards invoked a model")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Arguments.Tools = []string{"TaskUpdate"}
	})
	if err := parent.SetTerminalExpandedView(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	before, err := parent.Global.Get()
	if err != nil {
		t.Fatal(err)
	}
	var prepared nativeWorkflowAgentPrepared
	if err = parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		prepared, err = parent.prepareNativeWorkflowAgent(ctx, scope, workflow.AgentExecution{Prompt: "Actual Workflow callback scope", Options: protocol.NewJSONObject(), RunID: "wf_task_guard-actual", OnAgentID: func(string) error { return nil }, OnProgress: func(workflow.AgentProgress) error { return nil }})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer prepared.ref.DeliveryFinished()
	child, err := NewMainPrint(prepared.options)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if err = child.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if !child.options.DetachedAgent.workflowTaskViewParent || child.options.DetachedAgent.authParent != parent {
		t.Fatal("Actual Workflow producer did not retain its own callback")
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if err := child.expandNativeTerminalTaskView(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("Workflow admitted foreign Scope")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = child.expandNativeTerminalTaskView(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Workflow admitted absent Scope", err)
	}
	var retired *cooperative.Scope
	if err = child.Owner.Run(ctx, func(scope *cooperative.Scope) error { retired = scope; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = child.expandNativeTerminalTaskView(retired); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Workflow admitted retired Scope", err)
	}
	parent.Close()
	if err = child.Owner.Run(ctx, func(scope *cooperative.Scope) error { return child.expandNativeTerminalTaskView(scope) }); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("Actual closed parent callback was admitted", err)
	}
	after, err := parent.Global.Get()
	if err != nil || !bytes.Equal(rawMain(before), rawMain(after)) {
		t.Fatal("A rejected callback changed parent Global", err, string(rawMain(after)))
	}
	child.Close()
	if err = child.expandNativeTerminalTaskView(retired); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Closed child admitted stale callback", err)
	}
}

func TestActualWorkflowCancellationBeforeTaskResponseKeepsParentView(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		if !bytes.Contains(body, []byte(`"name":"TaskUpdate"`)) {
			t.Error("Actual workflow lost the real TaskUpdate catalog", string(body))
		}
		close(entered)
		<-request.Context().Done()
		close(canceled)
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
		options.Interactive = true
		options.Arguments.Tools = []string{"TaskUpdate"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := parent.SetTerminalExpandedView(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		_, err := parent.runNativeWorkflowAgent(runCtx, workflow.AgentExecution{Prompt: "Actual held TaskUpdate workflow", Options: protocol.NewJSONObject(), RunID: "wf_task_cancel-actual", OnAgentID: func(string) error { return nil }, OnProgress: func(workflow.AgentProgress) error { return nil }})
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stop()
	joinCtx, joinCancel := context.WithTimeout(ctx, 3*time.Second)
	defer joinCancel()
	select {
	case <-done:
	case <-joinCtx.Done():
		t.Fatal("Actual canceled Workflow did not join", joinCtx.Err())
	}
	select {
	case <-canceled:
	case <-joinCtx.Done():
		t.Fatal("Actual Workflow did not abort provider request", joinCtx.Err())
	}
	view, err := parent.TerminalExpandedView(ctx)
	global, globalErr := parent.Global.Get()
	if err != nil || globalErr != nil || view != "none" || nativeSettingsTruthy(global.Get("showExpandedTodos")) {
		t.Fatal("Canceled late TaskUpdate expanded parent", view, err, globalErr, string(rawMain(global)))
	}
}
