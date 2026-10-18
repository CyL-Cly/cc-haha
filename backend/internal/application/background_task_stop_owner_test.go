package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// The real MainPrint/controller owns Stop and its ok=false result. No model
// output or callback-success flag is substituted for that control boundary.
func TestDesktopBackgroundTaskStopCallbackUsesActualSuccessfulOwnerResult(t *testing.T) {
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			modelCalls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer model.Close()
	options := nativeSessionOptions(t, model.URL)
	var mu sync.Mutex
	calls := []string{}
	frames := []json.RawMessage{}
	live, err := DesktopFactory(options)(context.Background(), server.SessionOptions{ID: options.Arguments.SessionID, WorkDir: options.WorkDir, OnBackgroundTaskStopped: func(id string, gone bool) func() {
		return func() {
			mu.Lock()
			defer mu.Unlock()
			suffix := ":stopped"
			if gone {
				suffix = ":already-gone"
			}
			calls = append(calls, id+suffix)
		}
	}}, func(data json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		frames = append(frames, append(json.RawMessage(nil), data...))
	})
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	engine := d.currentEngine()
	taskCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	if err = engine.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, item := range []struct{ id, kind string }{{"owned-shell", "local_bash"}, {"unsupported-task", "fixture_unsupported"}} {
			entry := &sessioncontroller.BackgroundTask{Data: obj(raw(map[string]any{"id": item.id, "type": item.kind, "status": "running", "command": "private fixture"})), Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return raw(""), nil }, Join: func(context.Context) error { return nil }}
			if err := engine.process.Controller.RegisterBackgroundTaskOnOwner(scope, entry); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// An actual failed control is reported as a frame while Handle returns nil.
	if err = d.Handle(context.Background(), raw(map[string]any{"type": "stop_background_task", "taskId": "unsupported-task"})); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	failed := len(calls) == 0 && len(frames) > 0 && obj(frames[len(frames)-1]).String("type") == "background_task_stop_failed"
	mu.Unlock()
	if !failed {
		t.Fatal("ok=false was treated as a successful stop")
	}
	if err = d.Handle(context.Background(), raw(map[string]any{"type": "stop_background_task", "taskId": "owned-shell"})); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(taskCtx), context.Canceled) {
		t.Fatal("actual controller did not cancel the registered task")
	}
	if err = d.Handle(context.Background(), raw(map[string]any{"type": "stop_background_task", "taskId": "evicted-task"})); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	actual := append([]string{}, calls...)
	mu.Unlock()
	if !reflect.DeepEqual(actual, []string{"owned-shell:stopped", "evicted-task:already-gone"}) {
		t.Fatal("actual owner result callback changed", actual)
	}
	// Default-nil callback preserves the original alreadyGone projection.
	d.onBackgroundTaskStopped = nil
	if err = d.Handle(context.Background(), raw(map[string]any{"type": "stop_background_task", "taskId": "another-evicted-task"})); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	last := obj(frames[len(frames)-1])
	mu.Unlock()
	if last.String("type") != "system_notification" || obj(last.Get("data")).String("task_id") != "another-evicted-task" {
		t.Fatal("nil callback changed the existing projection", string(raw(last)))
	}
	// Keep the actual successful Stop receipt but publish a different real
	// engine before its acknowledgement returns. This exercises the same
	// d.mu/current-engine identity fence as native replacement, without
	// claiming to be a public set_runtime_config/model acceptance test.
	replacementOptions := nativeSessionOptions(t, model.URL)
	replacement, err := NewSession(context.Background(), replacementOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close(context.Background()) })
	t.Cleanup(func() { _ = engine.Close(context.Background()) })
	d.onBackgroundTaskStopped = func(id string, gone bool) func() {
		return func() {
			mu.Lock()
			calls = append(calls, "stale-ack:"+id)
			mu.Unlock()
		}
	}
	if err = engine.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		entry := &sessioncontroller.BackgroundTask{Data: obj(raw(map[string]any{"id": "retired-ack", "type": "local_workflow", "status": "running"})), Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return raw(""), nil }, Join: func(context.Context) error { return nil }}
		entry.Stop = func(current *cooperative.Scope, _ float64) error {
			if err := engine.process.Controller.UpdateBackgroundTaskOnOwner(current, "retired-ack", func(data protocol.JSONObject) (protocol.JSONObject, error) {
				data.Set("status", raw("killed"))
				return data, nil
			}); err != nil {
				return err
			}
			d.mu.Lock()
			d.engine = replacement
			d.mu.Unlock()
			return nil
		}
		return engine.process.Controller.RegisterBackgroundTaskOnOwner(scope, entry)
	}); err != nil {
		_ = replacement.Close(context.Background())
		t.Fatal(err)
	}
	if err = d.Handle(context.Background(), raw(map[string]any{"type": "stop_background_task", "taskId": "retired-ack"})); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	actual = append([]string{}, calls...)
	mu.Unlock()
	if !reflect.DeepEqual(actual, []string{"owned-shell:stopped", "evicted-task:already-gone"}) {
		t.Fatal("retired engine acknowledgement reached current owner", actual)
	}
	if modelCalls.Load() != 0 {
		t.Fatal("control-only fixture called a model", modelCalls.Load())
	}
}

func TestDesktopBackgroundTaskStopClaimsWhileEngineLockedThenPersistsUnlocked(t *testing.T) {
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			modelCalls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer model.Close()
	options := nativeSessionOptions(t, model.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	claimEntered, releaseClaim, workDone, controlDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClaim) }) }
	var claimCalls atomic.Int32
	var claimLocked, workUnlocked, claimEngineMatches atomic.Bool
	var claimIDMatches, ownerCancelledAtClaim atomic.Bool
	var claimTimedOut atomic.Bool
	var d *DesktopSession
	var original *Session
	taskCtx, taskCancel := context.WithCancelCause(context.Background())
	defer taskCancel(context.Canceled)
	live, err := DesktopFactory(options)(ctx, server.SessionOptions{
		ID: options.Arguments.SessionID, WorkDir: options.WorkDir,
		OnBackgroundTaskStopped: func(id string, alreadyGone bool) func() {
			claimCalls.Add(1)
			claimIDMatches.Store(id == "owned-claim-agent" && !alreadyGone)
			ownerCancelledAtClaim.Store(errors.Is(context.Cause(taskCtx), context.Canceled))
			// This TryLock inspects the actual DesktopSession mutex. A callback
			// merely being invoked is insufficient: the old engine must remain
			// protected continuously through this claim boundary.
			if d.mu.TryLock() {
				d.mu.Unlock()
			} else {
				claimLocked.Store(true)
				claimEngineMatches.Store(d.engine == original) // Caller owns d.mu here.
			}
			close(claimEntered)
			select {
			case <-releaseClaim:
			case <-ctx.Done():
				claimTimedOut.Store(true)
			}
			return func() {
				// This is the returned persistence work, not the claim. It must
				// be callable without retaining the application mutex, so a
				// server persistence/replay path can acquire that same mutex.
				if d.mu.TryLock() {
					workUnlocked.Store(true)
					d.mu.Unlock()
				}
				close(workDone)
			}
		},
	}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	d = live.(*DesktopSession)
	original = d.currentEngine()
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	controlStarted := false
	t.Cleanup(func() {
		release()
		cancel()
		if controlStarted {
			select {
			case <-controlDone:
			case <-time.After(3 * time.Second):
				t.Error("Stop owner control did not join after releasing claim")
			}
		}
	})
	// Register one real task in the actual controller. local_agent cancellation
	// updates that store and its actual context without an extra synthetic SDK
	// terminal producer racing the mutex probe. It makes no model/tool run claim.
	if err := original.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry := &sessioncontroller.BackgroundTask{
			Data:       obj(raw(map[string]any{"id": "owned-claim-agent", "type": "local_agent", "status": "running", "description": "owned claim fixture"})),
			Cancel:     taskCancel,
			ReadOutput: func(context.Context) (json.RawMessage, error) { return raw(""), nil },
			Join:       func(context.Context) error { return nil },
		}
		return original.process.Controller.RegisterBackgroundTaskOnOwner(scope, entry)
	}); err != nil {
		t.Fatal(err)
	}
	var controlErr error
	controlStarted = true
	go func() {
		controlErr = d.Handle(ctx, raw(map[string]any{"type": "stop_background_task", "taskId": "owned-claim-agent"}))
		close(controlDone)
	}()
	select {
	case <-claimEntered:
	case <-ctx.Done():
		t.Fatal("actual successful MainPrint/controller Stop did not enter claim", ctx.Err())
	}
	if !ownerCancelledAtClaim.Load() {
		t.Fatal("claim ran before the actual controller canceled its registered task")
	}
	// A separate goroutine's replacement-style lock attempt cannot cross the
	// gated claim. This uses TryLock rather than scheduler/timing inference.
	attempt := make(chan bool, 1)
	go func() {
		acquired := d.mu.TryLock()
		if acquired {
			d.mu.Unlock()
		}
		attempt <- acquired
	}()
	select {
	case acquired := <-attempt:
		if acquired {
			t.Fatal("another engine publication could acquire d.mu before claim completed")
		}
	case <-ctx.Done():
		t.Fatal("independent mutex attempt did not settle", ctx.Err())
	}
	var taskStatus string
	if err := original.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		task, err := original.process.Controller.BackgroundTaskOnOwner(scope, "owned-claim-agent")
		if err != nil {
			return err
		}
		if task == nil {
			return errors.New("actual stopped task disappeared before claim")
		}
		taskStatus = task.Data.String("status")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "killed" {
		t.Fatal("claim did not follow the actual controller success transition", taskStatus)
	}
	release()
	select {
	case <-workDone:
	case <-ctx.Done():
		t.Fatal("returned persistence work did not run", ctx.Err())
	}
	select {
	case <-controlDone:
	case <-ctx.Done():
		t.Fatal("actual successful Stop did not join", ctx.Err())
	}
	if controlErr != nil {
		t.Fatal(controlErr)
	}
	if claimCalls.Load() != 1 || !claimIDMatches.Load() || !claimLocked.Load() || !claimEngineMatches.Load() || !workUnlocked.Load() || claimTimedOut.Load() {
		t.Fatal("claim/work mutex boundary violated", claimCalls.Load(), claimIDMatches.Load(), claimLocked.Load(), claimEngineMatches.Load(), workUnlocked.Load(), claimTimedOut.Load())
	}
	if modelCalls.Load() != 0 {
		t.Fatal("control-only claim fixture called a model", modelCalls.Load())
	}
}
