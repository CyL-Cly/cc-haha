package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// This wrapper leaves the actual public Stop handler waiting for its owner
// receipt. The embedded owner still supplies the real Factory SDK/Emit seams;
// no test writes server task state or substitutes HTTP activity responses.
type liveStatusStopOrderOwner struct {
	*liveStatusPublicOwner
	entered chan struct{}
	ack     chan struct{}
	outcome string
}

func (o *liveStatusStopOrderOwner) Handle(ctx context.Context, raw json.RawMessage) error {
	var input struct {
		Type   string `json:"type"`
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	if input.Type != "stop_background_task" {
		return o.liveStatusPublicOwner.Handle(ctx, raw)
	}
	owned := append(json.RawMessage(nil), raw...)
	defer func() {
		select {
		case o.handled <- owned:
		default:
		}
	}()
	close(o.entered)
	select {
	case <-o.ack:
	case <-ctx.Done():
		return ctx.Err()
	}
	switch o.outcome {
	case "reported-failure":
		o.output(rawDisconnect(map[string]any{"type": "background_task_stop_failed", "taskId": input.TaskID, "message": "actual owned control rejected"}))
		return nil
	case "returned-error":
		return errors.New("actual owned control failed")
	default:
		o.mu.Lock()
		callback := o.onStopped
		o.mu.Unlock()
		if callback == nil {
			return errors.New("actual successful Stop owner callback unavailable")
		}
		callback(input.TaskID, false)
		return nil
	}
}
func liveStatusStopOrderRelease(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func TestLiveStatusPublicStopTerminalBeforeReceiptKeepsStrictBookend(t *testing.T) {
	for _, path := range []string{"projected-before-sdk", "sdk-only"} {
		t.Run(path, func(t *testing.T) {
			owner := &liveStatusStopOrderOwner{liveStatusPublicOwner: newLiveStatusPublicOwner(), entered: make(chan struct{}), ack: make(chan struct{})}
			gate := &liveStatusPublicPersistenceGate{entered: make(chan struct{}, 8), answers: make(chan error)}
			clock := &lifecycleClock{}
			options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
				owner.attach(o, emit)
				return owner, nil
			})
			options.PersistSession, options.AfterRuntimeTimer = gate.persist, clock.after
			server, host := testHost(t, options)
			t.Cleanup(gate.close)
			t.Cleanup(func() { liveStatusStopOrderRelease(owner.ack) })
			id := createTestSession(t, host, options.WorkDir)
			socket := liveStatusPublicWarm(t, host, id, owner.liveStatusPublicOwner)
			owner.mu.Lock()
			present := owner.onStopped != nil
			owner.mu.Unlock()
			if !present {
				t.Fatal("actual successful Stop owner callback not wired")
			}
			owner.SDK(`{"type":"system","subtype":"task_started","task_id":"early-agent","tool_use_id":"early-tool","task_type":"local_agent"}`)
			gate.active.Store(true)
			liveStatusPublicSend(t, socket, `{"type":"stop_background_task","taskId":"early-agent"}`)
			liveStatusPublicAwait(t, owner.entered, "public Stop did not reach the actual owner receipt boundary")
			terminal := `{"type":"system","subtype":"task_notification","task_id":"early-agent","tool_use_id":"early-tool","task_type":"local_agent","status":"completed"}`
			eventDone := make(chan struct{})
			go func() {
				defer close(eventDone)
				if path == "projected-before-sdk" {
					owner.output(rawDisconnect(map[string]any{"type": "system_notification", "subtype": "task_notification", "data": json.RawMessage(terminal)}))
				}
				owner.SDK(terminal)
			}()
			for attempt := 0; attempt < 3; attempt++ {
				liveStatusPublicAwait(t, gate.entered, "early terminal did not enter strict owner bookend persistence")
				liveStatusPublicWant(t, host, map[string]string{id: "running"})
				gate.answers <- errors.New("owned early-terminal persistence failure")
			}
			liveStatusPublicAwait(t, eventDone, "early terminal producer did not join after the three failed writes")
			liveStatusPublicEventually(t, host, map[string]string{})
			before, _ := liveStatusPublicCanonical(t, server, id)
			for _, row := range before {
				if row.TaskID == "early-agent" {
					t.Fatal("failed strict persistence leaked a normal completed/stopped bookend", row)
				}
			}
			// The actual Stop receipt is still held. A retry owns the same
			// stopped task and counts only while its real append is in flight.
			timer := clock.latest(t)
			if timer.delay != 250*time.Millisecond {
				t.Fatal("missing strict first retry", timer.delay)
			}
			retryDone := make(chan struct{})
			go func() { timer.callback(); close(retryDone) }()
			liveStatusPublicAwait(t, gate.entered, "early-terminal retry did not enter persistence")
			liveStatusPublicWant(t, host, map[string]string{id: "running"})
			gate.active.Store(false) // Let the append's second shouldPersist check proceed normally.
			gate.answers <- nil
			liveStatusPublicAwait(t, retryDone, "early-terminal retry did not join")
			liveStatusPublicWant(t, host, map[string]string{})
			liveStatusPublicOneStopped(t, server, id, "early-agent", "early-tool")
			liveStatusStopOrderRelease(owner.ack)
			liveStatusPublicHandled(t, owner.liveStatusPublicOwner, "stop_background_task")
			liveStatusPublicSend(t, socket, `{"type":"ping"}`)
			receiveType(t, socket, "pong")
			owner.SDK(terminal)
			owner.SDK(`{"type":"system","subtype":"task_progress","task_id":"early-agent"}`)
			owner.SDK(`{"type":"system","subtype":"task_started","task_id":"early-agent","task_type":"local_agent"}`)
			liveStatusPublicWant(t, host, map[string]string{})
			liveStatusPublicOneStopped(t, server, id, "early-agent", "early-tool")
		})
	}
}

func TestLiveStatusPublicFailedStopDoesNotRewriteLaterNormalCompletion(t *testing.T) {
	for _, outcome := range []string{"reported-failure", "returned-error"} {
		t.Run(outcome, func(t *testing.T) {
			owner := &liveStatusStopOrderOwner{liveStatusPublicOwner: newLiveStatusPublicOwner(), entered: make(chan struct{}), ack: make(chan struct{}), outcome: outcome}
			options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
				owner.attach(o, emit)
				return owner, nil
			})
			options.AfterRuntimeTimer = (&lifecycleClock{}).after
			server, host := testHost(t, options)
			t.Cleanup(func() { liveStatusStopOrderRelease(owner.ack) })
			id := createTestSession(t, host, options.WorkDir)
			socket := liveStatusPublicWarm(t, host, id, owner.liveStatusPublicOwner)
			owner.SDK(`{"type":"system","subtype":"task_started","task_id":"rejected-agent","tool_use_id":"rejected-tool","task_type":"local_agent"}`)
			owner.SDK(`{"type":"system","subtype":"task_started","task_id":"other-shell","tool_use_id":"other-tool","task_type":"local_bash"}`)
			liveStatusPublicSend(t, socket, `{"type":"stop_background_task","taskId":"rejected-agent"}`)
			liveStatusPublicAwait(t, owner.entered, "Stop request did not enter the exact owner")
			liveStatusPublicWant(t, host, map[string]string{id: "running"})
			liveStatusStopOrderRelease(owner.ack)
			liveStatusPublicHandled(t, owner.liveStatusPublicOwner, "stop_background_task")
			// Handle's defer precedes the server's error rollback. A public
			// reader barrier proves that rollback finished before the terminal.
			liveStatusPublicSend(t, socket, `{"type":"ping"}`)
			receiveType(t, socket, "pong")
			liveStatusPublicWant(t, host, map[string]string{id: "running"})
			owner.SDK(`{"type":"system","subtype":"task_notification","task_id":"rejected-agent","tool_use_id":"rejected-tool","status":"completed"}`)
			liveStatusPublicWant(t, host, map[string]string{id: "running"}) // The unrelated task is still owned and active.
			owner.SDK(`{"type":"system","subtype":"task_notification","task_id":"other-shell","tool_use_id":"other-tool","status":"completed"}`)
			liveStatusPublicWant(t, host, map[string]string{})
			rows, _ := liveStatusPublicCanonical(t, server, id)
			counts := map[string]int{}
			for _, row := range rows {
				if row.TaskID == "rejected-agent" || row.TaskID == "other-shell" {
					counts[row.TaskID]++
					if row.Status != "completed" {
						t.Fatal("failed Stop swallowed/relabelled a later normal terminal", row)
					}
					wantTool := "rejected-tool"
					if row.TaskID == "other-shell" {
						wantTool = "other-tool"
					}
					if row.ToolUseID != wantTool {
						t.Fatal("normal task identity changed", row)
					}
				}
			}
			if counts["rejected-agent"] != 1 || counts["other-shell"] != 1 {
				t.Fatal("normal completion not persisted exactly once", counts)
			}
		})
	}
}
