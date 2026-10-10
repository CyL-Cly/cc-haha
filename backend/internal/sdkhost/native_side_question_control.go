package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sidequestion"
)

type nativeSideQuestionTask struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type nativeSideQuestions struct {
	mu        sync.Mutex
	tasks     map[string]*nativeSideQuestionTask
	closed    bool
	work      sync.WaitGroup
	closeDone chan struct{}
}

func (h *Host) BeginSideQuestion(scope *cooperative.Scope, ctx context.Context, request protocol.JSONObject) (*OwnerFuture[protocol.JSONObject], error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || !h.nativeSideQuestionModels.Owns(h.options.Owner) || h.sideQuestions == nil {
		return nil, cooperative.ErrRestricted
	}
	state := h.sideQuestions
	id := nativeSideQuestionKey(request.Get("question_id"))
	future, e := NewOwnerFuture[protocol.JSONObject](h.options.Owner)
	if e != nil {
		return nil, e
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return nil, cooperative.ErrClosed
	}
	if state.tasks[id] != nil {
		state.mu.Unlock()
		_ = future.Reject(scope, errors.New("Side question already running"))
		return future, nil
	}
	active, cancelCause := context.WithCancelCause(ctx)
	cancel := func() { cancelCause(errors.New("Side question cancelled")) }
	task := &nativeSideQuestionTask{cancel: cancel, done: make(chan struct{})}
	state.tasks[id] = task
	state.work.Add(1)
	state.mu.Unlock()
	input := sidequestion.Input{QuestionID: id, Question: request.Get("question"), History: request.Get("history")}
	if input.History == nil {
		input.History = raw([]any{})
	}
	snapshot, e := h.sideQuestionSnapshot(scope, input)
	if e != nil {
		state.mu.Lock()
		delete(state.tasks, id)
		state.mu.Unlock()
		state.work.Done()
		close(task.done)
		cancel()
		_ = future.Reject(scope, e)
		return future, nil
	}
	// Admission/snapshot are synchronous; the sealed producer gives the Owner
	// back at each real await instead of nesting a whole model stack in Input.
	e = h.nativeSideQuestionModels.Start(scope, active, func(next *cooperative.Scope) error {
		value, failure := h.querySideQuestion(active, next, snapshot)
		if failure != nil {
			return future.Reject(next, failure)
		}
		return future.Resolve(next, value)
	}, func(next *cooperative.Scope, failure error) {
		if failure != nil {
			_ = future.RejectAdministrative(failure)
		}
		state.mu.Lock()
		if state.tasks[id] == task {
			delete(state.tasks, id)
		}
		state.mu.Unlock()
		cancel()
		close(task.done)
		state.work.Done()
	})
	if e != nil {
		state.mu.Lock()
		delete(state.tasks, id)
		state.mu.Unlock()
		cancel()
		close(task.done)
		state.work.Done()
		_ = future.Reject(scope, e)
	}
	return future, nil
}
func (h *Host) CancelSideQuestion(scope *cooperative.Scope, id string) (protocol.JSONObject, error) {
	return h.CancelSideQuestionJSON(scope, quote(id))
}
func (h *Host) CancelSideQuestionJSON(scope *cooperative.Scope, rawID json.RawMessage) (protocol.JSONObject, error) {
	id := nativeSideQuestionKey(rawID)
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.sideQuestions == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	h.sideQuestions.mu.Lock()
	task := h.sideQuestions.tasks[id]
	h.sideQuestions.mu.Unlock()
	if task != nil {
		task.cancel()
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "cancelled", Value: raw(task != nil)}), nil
}
func (h *Host) closeSideQuestions(ctx context.Context) error {
	if h.sideQuestions == nil {
		return nil
	}
	state := h.sideQuestions
	state.mu.Lock()
	if state.closeDone == nil {
		state.closed = true
		state.closeDone = make(chan struct{})
		for _, task := range state.tasks {
			task.cancel()
		}
		go func() { state.work.Wait(); close(state.closeDone) }()
	}
	done := state.closeDone
	state.mu.Unlock()
	select {
	case <-done:
		return h.nativeSideQuestionModels.Join(ctx)
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func (h *Host) sideQuestionControl(scope *cooperative.Scope, event protocol.JSONObject) (bool, error) {
	request := object(event.Get("request"))
	subtype := request.String("subtype")
	if subtype != "side_question" && subtype != "cancel_side_question" {
		return false, nil
	}
	id := event.Get("request_id")
	respond := func(next *cooperative.Scope, value protocol.JSONObject, failure error) error {
		response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: id}, protocol.JSONField{Name: "response", Value: raw(value)})
		if failure != nil {
			response.Set("subtype", quote("error"))
			response.Delete("response")
			response.Set("error", sidequestion.ErrorMessageJSON(failure))
		}
		_, e := h.enqueue(next, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)}))
		return e
	}
	if subtype == "cancel_side_question" {
		value, e := h.CancelSideQuestionJSON(scope, request.Get("question_id"))
		return true, respond(scope, value, e)
	}
	if request.Get("question_id") == nil {
		request.Set("question_id", id)
	}
	future, e := h.BeginSideQuestion(scope, context.Background(), request)
	if e != nil {
		return true, respond(scope, protocol.NewJSONObject(), e)
	}
	_, e = future.Then(scope, func(next *cooperative.Scope, value protocol.JSONObject, failure error) error {
		return respond(next, value, failure)
	})
	return true, e
}

func (h *Host) CloseSideQuestions(ctx context.Context) error { return h.closeSideQuestions(ctx) }

func nativeSideQuestionKey(rawID json.RawMessage) string {
	return string(protocol.StringifyValidatedJavaScriptJSON(rawID))
}
