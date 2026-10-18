package clibootstrap

// These Controller receipt tests require the startup-compensation v2 API.
// They do not start an Agent, make a model request, or invoke Source Bun.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func nativeAgentReceiptMain(t *testing.T) *MainPrint {
	t.Helper()
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "registration-only fixture forbids HTTP", http.StatusInternalServerError)
	}))
	t.Cleanup(peer.Close)
	// Registered before the existing MainPrint harness so this assertion runs
	// after its ShutdownSession, not just before teardown.
	t.Cleanup(func() {
		if count := requests.Load(); count != 0 {
			t.Errorf("registration-only fixture performed %d HTTP requests", count)
		}
	})
	return backgroundAgentMain(t, peer.URL)
}

func nativeAgentReceiptTask(t *testing.T, data, label string, calls *[]string) *sessioncontroller.BackgroundTask {
	t.Helper()
	record := func(value string) { *calls = append(*calls, label+":"+value) }
	return &sessioncontroller.BackgroundTask{
		Data: taskObject(t, []byte(data)),
		Cancel: func(cause error) {
			record("cancel:" + fmt.Sprint(cause))
		},
		WorkCancel: func(cause error) {
			record("work-cancel:" + fmt.Sprint(cause))
		},
		IdleCallbacks: []func(){func() { record("idle-1") }, func() { record("idle-2") }},
		ReadOutput: func(context.Context) (json.RawMessage, error) {
			record("output")
			return quoteMain(label + ":output"), nil
		},
		ReadActiveOutput: func(context.Context) (json.RawMessage, error) {
			record("active-output")
			return quoteMain(label + ":active-output"), nil
		},
		ReadDelta: func(_ context.Context, offset int64) (bool, int64, error) {
			record(fmt.Sprintf("delta:%d", offset))
			return true, offset + 11, nil
		},
		Join: func(context.Context) error { record("join"); return nil },
		Promote: func(*cooperative.Scope) error {
			record("promote")
			return nil
		},
		Stop: func(_ *cooperative.Scope, now float64) error {
			record(fmt.Sprintf("stop:%.0f", now))
			return nil
		},
		WorkflowControl: func(_ *cooperative.Scope, kind, value string) error {
			record("workflow:" + kind + ":" + value)
			return nil
		},
	}
}

func nativeAgentReceiptExpectTask(c *sessioncontroller.Controller, scope *cooperative.Scope, id, want string) (*sessioncontroller.BackgroundTask, error) {
	task, err := c.BackgroundTaskOnOwner(scope, id)
	if err != nil {
		return nil, err
	}
	if want == "" {
		if task != nil {
			return nil, fmt.Errorf("task %q survived rollback: %s", id, rawMain(task.Data))
		}
		return nil, nil
	}
	if task == nil || string(rawMain(task.Data)) != want {
		return nil, fmt.Errorf("task %q did not match its complete literal fixture: got %#v", id, task)
	}
	return task, nil
}

func nativeAgentReceiptExpectAlias(c *sessioncontroller.Controller, scope *cooperative.Scope, name, want string) error {
	got, err := c.ResolveAgentNameOnOwner(scope, name)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("alias %q = %q, want %q", name, got, want)
	}
	return nil
}

func TestNativeAgentRegistrationReceiptRestoresExactEntryCallbacksOrderAndAlias(t *testing.T) {
	m := nativeAgentReceiptMain(t)
	var calls []string
	const oldData = `{"id":"receipt-middle","type":"local_agent","status":"completed","description":"original card","unknown":{"version":7,"list":[null,"keep",false]},"messages":[{"uuid":"old-message"}],"pendingMessages":null}`
	old := nativeAgentReceiptTask(t, oldData, "old", &calls)
	replacement := nativeAgentReceiptTask(t, `{"id":"receipt-middle","type":"local_agent","status":"completed","retain":true,"description":"startup card","unknown":{"replacement":true},"toolUseId":"new-tool","startTime":99}`, "startup", &calls)
	before := nativeAgentReceiptTask(t, `{"id":"receipt-before","type":"local_agent","status":"completed"}`, "before", &calls)
	after := nativeAgentReceiptTask(t, `{"id":"receipt-after","type":"local_agent","status":"completed"}`, "after", &calls)
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, task := range []*sessioncontroller.BackgroundTask{before, old, after} {
			if err := m.Controller.RegisterBackgroundTaskOnOwner(scope, task); err != nil {
				return err
			}
		}
		if err := m.Controller.RegisterAgentNameOnOwner(scope, "receipt-worker", "receipt-before"); err != nil {
			return err
		}
		receipt, err := m.Controller.RegisterAgentTaskOnOwner(scope, replacement, "receipt-worker")
		if err != nil {
			return err
		}
		if receipt == nil || !receipt.Replaced || receipt.Commit == nil || receipt.Rollback == nil {
			return fmt.Errorf("replacement did not return an actual reversible receipt: %#v", receipt)
		}
		if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-middle", `{"id":"receipt-middle","type":"local_agent","status":"completed","retain":true,"description":"startup card","unknown":{"replacement":true},"toolUseId":"new-tool","startTime":99}`); err != nil {
			return err
		}
		if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "receipt-worker", "receipt-middle"); err != nil {
			return err
		}
		for attempt := 0; attempt < 2; attempt++ {
			if err := receipt.Rollback(scope); err != nil {
				return err
			}
			if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-middle", oldData); err != nil {
				return err
			}
			if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "receipt-worker", "receipt-before"); err != nil {
				return err
			}
		}
		rows, err := m.Controller.BackgroundTasksOnOwner(scope)
		if err != nil {
			return err
		}
		ids := make([]string, len(rows))
		for index, row := range rows {
			ids[index] = row.String("id")
		}
		if strings.Join(ids, ",") != "receipt-before,receipt-middle,receipt-after" {
			return fmt.Errorf("rollback changed original task order: %v", ids)
		}
		restored, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-middle", oldData)
		if err != nil {
			return err
		}
		if restored.Data.Get("retain") != nil || len(restored.IdleCallbacks) != 2 || restored.Cancel == nil || restored.WorkCancel == nil || restored.ReadOutput == nil || restored.ReadActiveOutput == nil || restored.ReadDelta == nil || restored.Join == nil || restored.Promote == nil || restored.Stop == nil || restored.WorkflowControl == nil {
			return errors.New("rollback lost missing retain or one of the original callback capabilities")
		}
		restored.Cancel(errors.New("cancel-cause"))
		restored.WorkCancel(errors.New("work-cause"))
		for _, callback := range restored.IdleCallbacks {
			callback()
		}
		output, err := restored.ReadOutput(context.Background())
		if err != nil || string(output) != `"old:output"` {
			return fmt.Errorf("wrong restored output: %s, %v", output, err)
		}
		output, err = restored.ReadActiveOutput(context.Background())
		if err != nil || string(output) != `"old:active-output"` {
			return fmt.Errorf("wrong restored active output: %s, %v", output, err)
		}
		changed, offset, err := restored.ReadDelta(context.Background(), 31)
		if err != nil || !changed || offset != 42 {
			return fmt.Errorf("wrong restored delta: %v, %d, %v", changed, offset, err)
		}
		if err := restored.Join(context.Background()); err != nil {
			return err
		}
		if err := restored.Promote(scope); err != nil {
			return err
		}
		if err := restored.Stop(scope, 42); err != nil {
			return err
		}
		if err := restored.WorkflowControl(scope, "signal", "pause"); err != nil {
			return err
		}
		if strings.Join(calls, "|") != "old:cancel:cancel-cause|old:work-cancel:work-cause|old:idle-1|old:idle-2|old:output|old:active-output|old:delta:31|old:join|old:promote|old:stop:42|old:workflow:signal:pause" {
			return fmt.Errorf("rollback changed callback identities or idle order: %v", calls)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAgentRegistrationReceiptCannotUndoCommittedOrIndependentlyReplacedTask(t *testing.T) {
	m := nativeAgentReceiptMain(t)
	var calls []string
	temporary := nativeAgentReceiptTask(t, `{"id":"receipt-temporary","type":"local_agent","status":"completed"}`, "temporary", &calls)
	const committedData = `{"id":"receipt-committed","type":"local_agent","status":"completed","retain":true,"unknown":"committed"}`
	committed := nativeAgentReceiptTask(t, committedData, "committed", &calls)
	old := nativeAgentReceiptTask(t, `{"id":"receipt-handoff","type":"local_agent","status":"completed","stage":"old"}`, "old", &calls)
	startup := nativeAgentReceiptTask(t, `{"id":"receipt-handoff","type":"local_agent","status":"completed","stage":"startup"}`, "startup", &calls)
	const independentData = `{"id":"receipt-handoff","type":"local_agent","status":"completed","stage":"independent","unknown":{"newOwner":true},"retain":false}`
	independent := nativeAgentReceiptTask(t, independentData, "independent", &calls)
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		fresh, err := m.Controller.RegisterAgentTaskOnOwner(scope, temporary, "temporary-name")
		if err != nil {
			return err
		}
		if fresh.Replaced {
			return errors.New("new task was reported as a replacement")
		}
		for attempt := 0; attempt < 2; attempt++ {
			if err := fresh.Rollback(scope); err != nil {
				return err
			}
			if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-temporary", ""); err != nil {
				return err
			}
			if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "temporary-name", ""); err != nil {
				return err
			}
		}
		receipt, err := m.Controller.RegisterAgentTaskOnOwner(scope, committed, "committed-name")
		if err != nil {
			return err
		}
		if receipt.Replaced {
			return errors.New("committed new task was reported as a replacement")
		}
		receipt.Commit()
		receipt.Commit()
		for attempt := 0; attempt < 2; attempt++ {
			if err := receipt.Rollback(scope); err != nil {
				return err
			}
			if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-committed", committedData); err != nil {
				return err
			}
			if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "committed-name", "receipt-committed"); err != nil {
				return err
			}
		}
		if err := m.Controller.RegisterBackgroundTaskOnOwner(scope, old); err != nil {
			return err
		}
		if err := m.Controller.RegisterAgentNameOnOwner(scope, "handoff-name", "receipt-committed"); err != nil {
			return err
		}
		pending, err := m.Controller.RegisterAgentTaskOnOwner(scope, startup, "handoff-name")
		if err != nil {
			return err
		}
		if !pending.Replaced {
			return errors.New("existing handoff task was not reported as replaced")
		}
		if err := m.Controller.RegisterBackgroundTaskOnOwner(scope, independent); err != nil {
			return err
		}
		// A separately published alias is no longer in the old startup's custody.
		if err := m.Controller.RegisterAgentNameOnOwner(scope, "handoff-name", "receipt-handoff"); err != nil {
			return err
		}
		for attempt := 0; attempt < 2; attempt++ {
			if err := pending.Rollback(scope); err != nil {
				return err
			}
			current, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-handoff", independentData)
			if err != nil {
				return err
			}
			output, err := current.ReadOutput(context.Background())
			if err != nil || string(output) != `"independent:output"` {
				return fmt.Errorf("obsolete receipt replaced a newer task's callback: %s, %v", output, err)
			}
			if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "handoff-name", "receipt-handoff"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAgentRegistrationReceiptRejectsForeignAndStaleScopeButAllowsCancelledOwnerFinally(t *testing.T) {
	m := nativeAgentReceiptMain(t)
	var calls []string
	const oldData = `{"id":"receipt-authority","type":"local_agent","status":"completed","unknown":{"authority":"original"}}`
	const newData = `{"id":"receipt-authority","type":"local_agent","status":"completed","retain":true,"unknown":{"authority":"startup"}}`
	old := nativeAgentReceiptTask(t, oldData, "old", &calls)
	startup := nativeAgentReceiptTask(t, newData, "startup", &calls)
	forbidden := nativeAgentReceiptTask(t, `{"id":"receipt-forbidden","type":"local_agent","status":"completed"}`, "forbidden", &calls)
	var stale *cooperative.Scope
	var receipt *sessioncontroller.BackgroundTaskRegistration
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		if err := m.Controller.RegisterBackgroundTaskOnOwner(scope, old); err != nil {
			return err
		}
		if err := m.Controller.RegisterAgentNameOnOwner(scope, "authority-name", "previous-alias"); err != nil {
			return err
		}
		var err error
		receipt, err = m.Controller.RegisterAgentTaskOnOwner(scope, startup, "authority-name")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if rejected, err := m.Controller.RegisterAgentTaskOnOwner(stale, forbidden, "forbidden-name"); !errors.Is(err, cooperative.ErrRestricted) || rejected != nil {
		t.Fatal("stale scope registered a task", rejected, err)
	}
	if err := receipt.Rollback(stale); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("stale scope consumed the startup receipt", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if rejected, err := m.Controller.RegisterAgentTaskOnOwner(scope, forbidden, "forbidden-name"); !errors.Is(err, cooperative.ErrRestricted) || rejected != nil {
			return fmt.Errorf("foreign scope registered a task: %v, %v", rejected, err)
		}
		if err := receipt.Rollback(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign scope consumed the startup receipt: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("caller cancelled before startup finally")
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-authority", newData); err != nil {
			return err
		}
		if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-forbidden", ""); err != nil {
			return err
		}
		if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "authority-name", "receipt-authority"); err != nil {
			return err
		}
		if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "forbidden-name", ""); err != nil {
			return err
		}
		// The rejected scopes must not consume the pending receipt. First use
		// it legitimately, then start a new admission on this exact stack so
		// its cancellation case really exercises the original finally.
		if err := receipt.Rollback(scope); err != nil {
			return err
		}
		if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-authority", oldData); err != nil {
			return err
		}
		if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "authority-name", "previous-alias"); err != nil {
			return err
		}
		cancelledReceipt, err := m.Controller.RegisterAgentTaskOnOwner(scope, startup, "authority-name")
		if err != nil {
			return err
		}
		cancel(cause)
		if !scope.BelongsTo(m.Owner) {
			return errors.New("cancellation invalidated the still-running Owner scope")
		}
		businessRan := false
		if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { businessRan = true; return nil }); !errors.Is(err, cause) || businessRan {
			return fmt.Errorf("fixture did not reach cancelled-but-valid scope: ran=%v, err=%v", businessRan, err)
		}
		// This is the admitted stack's original finally. It is not an attempt
		// to start new work or to resurrect an expired Owner scope.
		if err := cancelledReceipt.Rollback(scope); err != nil {
			return fmt.Errorf("cancelled Owner could not finish its admitted rollback: %w", err)
		}
		return cancelledReceipt.Rollback(scope)
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-authority", oldData); err != nil {
			return err
		}
		if _, err := nativeAgentReceiptExpectTask(m.Controller, scope, "receipt-forbidden", ""); err != nil {
			return err
		}
		if err := nativeAgentReceiptExpectAlias(m.Controller, scope, "authority-name", "previous-alias"); err != nil {
			return err
		}
		return nativeAgentReceiptExpectAlias(m.Controller, scope, "forbidden-name", "")
	}); err != nil {
		t.Fatal(err)
	}
}
