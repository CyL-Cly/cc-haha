package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestWorkflowStopSeamKeepsActualControllerStateAndTaskSpecificOrder(t *testing.T) {
	m := taskMainFixture(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	order := []string{}
	e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		id := "workflow-owned-stop"
		task := &sessioncontroller.BackgroundTask{Data: protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoteMain(id)}, protocol.JSONField{Name: "type", Value: quoteMain("local_workflow")}, protocol.JSONField{Name: "status", Value: quoteMain("running")}, protocol.JSONField{Name: "description", Value: quoteMain("Original workflow summary")}, protocol.JSONField{Name: "future", Value: []byte(`{"keep":true}`)}), Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return []byte(`""`), nil }, Join: func(context.Context) error { <-ctx.Done(); return nil }}
		task.Stop = func(current *cooperative.Scope, now float64) error {
			if e := m.Controller.UpdateBackgroundTaskOnOwner(current, id, func(data protocol.JSONObject) (protocol.JSONObject, error) {
				order = append(order, "settle")
				data.Set("status", quoteMain("killed"))
				data.Set("endTime", rawMain(now))
				return data, nil
			}); e != nil {
				return e
			}
			order = append(order, "abort-agent-controllers")
			cancel(context.Canceled)
			order = append(order, "notify")
			return m.Controller.EnqueueTaskNotificationOnOwner(current, "", "workflow notification")
		}
		if e := m.Controller.RegisterBackgroundTaskOnOwner(scope, task); e != nil {
			return e
		}
		copy, e := m.Controller.BackgroundTaskOnOwner(scope, id)
		if e != nil {
			return e
		}
		if copy.Stop == nil {
			return fmt.Errorf("lookup dropped original task-specific stop callback")
		}
		result, e := m.Controller.StopBackgroundTaskOnOwner(scope, id, 1700000000000)
		if e != nil {
			return e
		}
		if result.String("taskType") != "local_workflow" || result.String("command") != "Original workflow summary" {
			return fmt.Errorf("original TaskStop result changed: %s", rawMain(result))
		}
		copy, e = m.Controller.BackgroundTaskOnOwner(scope, id)
		if e != nil {
			return e
		}
		if copy.Data.String("status") != "killed" || string(copy.Data.Get("future")) != `{"keep":true}` {
			return fmt.Errorf("actual same AppState mutation lost state: %s", rawMain(copy.Data))
		}
		if _, e := m.Controller.StopBackgroundTaskOnOwner(scope, id, 1700000000001); e == nil {
			return fmt.Errorf("terminal Workflow task was stopped twice")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(order, []string{"settle", "abort-agent-controllers", "notify"}) || ctx.Err() != context.Canceled {
		t.Fatal("workflow callback/real cancellation order changed", order, ctx.Err())
	}
}
