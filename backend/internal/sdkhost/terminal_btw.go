package sdkhost

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The terminal component borrows the same side-question producer used by the
// public desktop control. Its unmount receipt also observes the actual worker.
func (h *Host) BeginTerminalBtw(scope *cooperative.Scope, ctx context.Context, id, question string) (*OwnerFuture[protocol.JSONObject], <-chan struct{}, error) {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.closing {
		return nil, nil, cooperative.ErrRestricted
	}
	request := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("side_question")}, protocol.JSONField{Name: "question_id", Value: quote(id)}, protocol.JSONField{Name: "question", Value: quote(question)}, protocol.JSONField{Name: "history", Value: raw([]any{})})
	future, err := h.BeginSideQuestion(scope, ctx, request)
	if err != nil {
		return nil, nil, err
	}
	h.sideQuestions.mu.Lock()
	task := h.sideQuestions.tasks[nativeSideQuestionKey(request.Get("question_id"))]
	h.sideQuestions.mu.Unlock()
	if task != nil {
		return future, task.done, nil
	}
	done := make(chan struct{})
	close(done)
	return future, done, nil
}

func (h *Host) CompleteTerminalBtwUsage(scope *cooperative.Scope, args string, fullscreen bool) ([]protocol.JSONObject, bool, error) {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.closing || protocol.TrimJavaScriptString(args) != "" {
		return nil, false, cooperative.ErrRestricted
	}
	immediate := h.askRunning
	if immediate && fullscreen {
		return nil, true, nil
	}
	output := "<local-command-stdout>Usage: /btw <your question></local-command-stdout>"
	if immediate {
		output = "<local-command-stdout>Usage: /btw &lt;your question&gt;</local-command-stdout>"
	}
	records := []protocol.JSONObject{h.options.Factory.CreateLocalCommandInput("<command-name>/btw</command-name>\n            <command-message>btw</command-message>\n            <command-args>" + args + "</command-args>"), h.options.Factory.CreateLocalCommandInput(output)}
	if err := h.serial.AppendTerminalBtwUsage(scope, records); err != nil {
		return nil, immediate, err
	}
	return records, immediate, nil
}

func (h *Host) TerminalBtwImmediateOnOwner(scope *cooperative.Scope) (bool, error) {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.closing {
		return false, cooperative.ErrRestricted
	}
	return h.askRunning, nil
}
