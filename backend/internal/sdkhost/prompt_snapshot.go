package sdkhost

import (
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/promptsnapshot"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) promptSnapshotPath(scope *cooperative.Scope) string {
	if h.options.Trace == nil || h.options.Trace.PromptSnapshots == nil || !promptsnapshot.Enabled(h.options.OriginalEnv) {
		return ""
	}
	writer := h.options.Controller.Writer()
	if writer == nil {
		return ""
	}
	path, err := writer.ComputedTranscriptPath(scope)
	if err != nil {
		return ""
	}
	if filepath.Base(filepath.Dir(path)) == "subagents" {
		return filepath.Join(filepath.Dir(filepath.Dir(path)), "prompt-snapshots.jsonl")
	}
	return filepath.Join(filepath.Dir(path), h.options.Controller.SessionID(), "prompt-snapshots.jsonl")
}
func (h *Host) promptSnapshotObserver(scope *cooperative.Scope) func(json.RawMessage) {
	path := h.promptSnapshotPath(scope)
	if path == "" {
		return nil
	}
	agent, query := h.agentID(), h.querySource()
	return func(body json.RawMessage) { h.options.Trace.PromptSnapshots.RecordRequest(path, agent, query, body) }
}
func (h *Host) recordUserContextSnapshot(scope *cooperative.Scope, context protocol.JSONObject) {
	path := h.promptSnapshotPath(scope)
	if path == "" {
		return
	}
	var values map[string]json.RawMessage
	_ = json.Unmarshal(raw(context), &values)
	strings := map[string]string{}
	for key, value := range values {
		var s string
		if json.Unmarshal(value, &s) == nil {
			strings[key] = s
		}
	}
	h.options.Trace.PromptSnapshots.RecordContext(path, h.agentID(), strings)
}
