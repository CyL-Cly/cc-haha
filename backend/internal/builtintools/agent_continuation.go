package builtintools

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// QueueAgentPendingMessage is Source's API-input queue, separate from the
// retained transcript display and from the root session-message inbox.
func QueueAgentPendingMessage(b *BackgroundTaskBindings, scope *cooperative.Scope, id, message string) error {
	return QueueAgentPendingMessageJSON(b, scope, id, quote(message))
}
func QueueAgentPendingMessageJSON(b *BackgroundTaskBindings, scope *cooperative.Scope, id string, message json.RawMessage) error {
	if _, e := protocol.DecodeJavaScriptJSONString(message); e != nil {
		return e
	}
	return b.Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		if task.String("type") != "local_agent" {
			return task, nil
		}
		var rows []json.RawMessage
		if e := json.Unmarshal(task.Get("pendingMessages"), &rows); e != nil {
			return task, e
		}
		task.Set("pendingMessages", raw(append(rows, append(json.RawMessage(nil), message...))))
		return task, nil
	})
}

func DrainAgentPendingAttachments(b *BackgroundTaskBindings, scope *cooperative.Scope, id string) ([]protocol.JSONObject, error) {
	out := []protocol.JSONObject{}
	if id == "" {
		return out, nil
	}
	err := b.Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		if task.String("type") != "local_agent" {
			return task, nil
		}
		var rows []json.RawMessage
		if e := json.Unmarshal(task.Get("pendingMessages"), &rows); e != nil {
			return task, e
		}
		if len(rows) == 0 {
			return task, nil
		}
		for _, prompt := range rows {
			out = append(out, protocol.NewJSONObject(field("type", "queued_command"), protocol.JSONField{Name: "prompt", Value: prompt}, field("origin", protocol.NewJSONObject(field("kind", "coordinator"))), field("isMeta", true)))
		}
		task.Set("pendingMessages", raw([]string{}))
		return task, nil
	})
	return out, err
}

func AppendRetainedAgentMessage(b *BackgroundTaskBindings, scope *cooperative.Scope, id string, message protocol.JSONObject) error {
	return b.Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		if task.String("type") != "local_agent" || string(task.Get("retain")) != "true" {
			return task, nil
		}
		var rows []protocol.JSONObject
		if v := task.Get("messages"); v != nil {
			if e := json.Unmarshal(v, &rows); e != nil {
				return task, e
			}
		}
		task.Set("messages", raw(append(rows, message.Clone())))
		return task, nil
	})
}
