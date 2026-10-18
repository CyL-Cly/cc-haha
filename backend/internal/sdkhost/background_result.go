package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) holdNativeBackgroundResult(scope *cooperative.Scope, result protocol.JSONObject) (bool, error) {
	if h.options.Print == nil || !h.options.Print.NativeProcess || h.isAgent() || h.options.RunState == nil {
		return false, nil
	}
	tasks, e := h.options.RunState.GetTasks(scope)
	if e != nil {
		return false, e
	}
	for _, task := range tasks {
		if task.Running && task.Background && (task.Type == "local_agent" || task.Type == "local_workflow") {
			h.heldBackResult = result.Clone()
			return true, nil
		}
	}
	h.heldBackResult = protocol.JSONObject{}
	return false, nil
}

// Print drains its canonical commands, waits for the actual background group,
// and only then releases the latest held result. A follow-up ASK may already
// have replaced it, exactly as Source's heldBackResult variable does.
func (h *Host) FlushHeldBackgroundResult(scope *cooperative.Scope) error {
	if !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if !h.heldBackResult.IsObject() {
		return nil
	}
	tasks, e := h.options.RunState.GetTasks(scope)
	if e != nil {
		return e
	}
	for _, task := range tasks {
		if task.Running && task.Background && task.Type != "in_process_teammate" {
			return nil
		}
	}
	result := h.heldBackResult
	h.heldBackResult = protocol.JSONObject{}
	_, e = h.enqueue(scope, result)
	if e == nil {
		e = h.flushSDKPromptSuggestion(scope)
	}
	return e
}
