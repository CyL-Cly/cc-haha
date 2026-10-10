package clibootstrap

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"testing"
	"time"
)

func TestActualInteractiveWorkflowChildPreparationDuringParentContinuation(t *testing.T) {
	m := ultracodeMain(t, "http://127.0.0.1:1", "", true, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		done := make(chan struct{})
		var failure error
		go func() {
			defer close(done)
			var prepared nativeWorkflowAgentPrepared
			r, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(current *cooperative.Scope) error {
				var e error
				prepared, e = m.prepareNativeWorkflowAgent(ctx, current, workflow.AgentExecution{Prompt: "private diagnostic", Options: protocol.NewJSONObject(), RunID: "wf_diag-abc", Index: 1, OnAgentID: func(string) error { return nil }, OnProgress: func(workflow.AgentProgress) error { return nil }})
				return e
			})
			if e == nil {
				e = r.Wait()
			}
			if e != nil {
				failure = fmt.Errorf("actual prepare: %w", e)
				return
			}
			defer prepared.ref.DeliveryFinished()
			child, e := NewMainPrint(prepared.options)
			if e != nil {
				failure = fmt.Errorf("actual child construct: %w", e)
				return
			}
			defer child.ShutdownSession(context.Background())
			if e = child.StartSession(ctx); e != nil {
				failure = fmt.Errorf("actual child start: %w", e)
				return
			}
			if e = child.Owner.Run(ctx, func(childScope *cooperative.Scope) error { return nil }); e != nil {
				failure = fmt.Errorf("actual child owned callback: %w", e)
				return
			}
		}()
		if e := scope.AwaitPromise(done); e != nil {
			return e
		}
		return failure
	}); e != nil {
		t.Fatal(e)
	}
}
