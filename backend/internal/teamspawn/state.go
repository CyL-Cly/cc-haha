package teamspawn

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const UIMessagesCap = 50

// AppendMessage keeps the original UI-only mirror bounded. The independent
// query history and physical transcript retain the full original envelopes.
func AppendMessage(task protocol.JSONObject, message protocol.JSONObject) (protocol.JSONObject, error) {
	var rows []protocol.JSONObject
	if value := task.Get("messages"); value != nil && string(value) != "null" {
		if err := json.Unmarshal(value, &rows); err != nil {
			return task, err
		}
	}
	if len(rows) >= UIMessagesCap {
		rows = rows[len(rows)-(UIMessagesCap-1):]
	}
	rows = append(rows, message.Clone())
	task = task.Clone()
	task.Set("messages", rawJSON(rows))
	return task, nil
}

// ObserveMessage is the original inProcessRunner AppState update. IDs use an
// ordered JSON array as the native representation of Source's insertion Set.
func ObserveMessage(task, message, progress protocol.JSONObject) (protocol.JSONObject, error) {
	var ids []string
	if value := task.Get("inProgressToolUseIDs"); value != nil {
		if err := json.Unmarshal(value, &ids); err != nil {
			return task, err
		}
	}
	defined := task.Get("inProgressToolUseIDs") != nil
	blocks, err := originalBlocks(message)
	if err != nil {
		return task, err
	}
	for _, value := range blocks {
		block := inboxObject(value)
		if message.String("type") == "assistant" && block.String("type") == "tool_use" {
			defined = true
			id := block.String("id")
			found := false
			for _, previous := range ids {
				if previous == id {
					found = true
					break
				}
			}
			if !found {
				ids = append(ids, id)
			}
		} else if message.String("type") == "user" && block.String("type") == "tool_result" && defined {
			id := block.String("tool_use_id")
			next := make([]string, 0, len(ids))
			for _, previous := range ids {
				if previous != id {
					next = append(next, previous)
				}
			}
			ids = next
		}
	}
	task, err = AppendMessage(task, message)
	if err != nil {
		return task, err
	}
	task.Set("progress", rawJSON(progress))
	if defined {
		if ids == nil {
			ids = []string{}
		}
		task.Set("inProgressToolUseIDs", rawJSON(ids))
	}
	return task, nil
}

// FinishTask preserves an earlier killed/completed task, closing its SDK
// bookend once. The caller then evicts the consumed terminal task as Source.
func FinishTask(task protocol.JSONObject, status string, now float64, failure string) (protocol.JSONObject, bool, error) {
	if task.String("status") != "running" {
		return task, false, nil
	}
	task = task.Clone()
	task.Set("status", quoted(status))
	task.Set("notified", rawJSON(true))
	task.Set("endTime", rawJSON(now))
	if status == "failed" {
		task.Set("error", quoted(failure))
		task.Set("isIdle", rawJSON(true))
	}
	var messages []protocol.JSONObject
	if value := task.Get("messages"); value != nil && string(value) != "null" {
		if err := json.Unmarshal(value, &messages); err != nil {
			return task, false, err
		}
	}
	if len(messages) > 0 {
		task.Set("messages", rawJSON(messages[len(messages)-1:]))
	} else {
		task.Delete("messages")
	}
	task.Set("pendingUserMessages", rawJSON([]string{}))
	task.Set("onIdleCallbacks", rawJSON([]string{}))
	for _, key := range []string{"inProgressToolUseIDs", "abortController", "unregisterCleanup", "currentWorkAbortController"} {
		task.Delete(key)
	}
	return task, true, nil
}
