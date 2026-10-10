package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// Notifications join the existing canonical command queue. Task output does
// not create another ASK, and next/later priorities retain their Source meaning.
func (c *Controller) EnqueueTaskNotificationOnOwner(scope *cooperative.Scope, agentID, content string) error {
	root, e := c.todosController(scope)
	if e != nil {
		return e
	}
	priority := "later"
	if !strings.Contains(content, "<status>") {
		priority = "next"
	}
	return root.enqueueOwned(scope, command{Content: quote(content), UUID: quote(root.options.UUID()), Priority: priority, Meta: true, Mode: "task-notification", AgentID: agentID})
}
func (c *Controller) TakeTaskNotificationAttachmentsOnOwner(scope *cooperative.Scope, agentID string) ([]protocol.JSONObject, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return nil, e
	}
	out := []protocol.JSONObject{}
	retained := root.queue[:0]
	for _, item := range root.queue {
		// Source without Sleep only admits next-priority commands mid-round. Local
		// shell terminal notifications are later and are drained after human ASK.
		if item.Mode != "task-notification" || item.AgentID != agentID || item.Priority == "later" {
			retained = append(retained, item)
			continue
		}
		payload := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("queued_command")}, protocol.JSONField{Name: "prompt", Value: item.Content}, protocol.JSONField{Name: "source_uuid", Value: item.UUID}, protocol.JSONField{Name: "imagePasteIds", Value: raw([]string{})}, protocol.JSONField{Name: "commandMode", Value: quote("task-notification")}, protocol.JSONField{Name: "isMeta", Value: raw(true)})
		out = append(out, payload)
		if e = root.logQueueOwned(scope, "remove", nil); e != nil {
			return nil, e
		}
	}
	root.queue = retained
	return out, nil
}
func (c *Controller) HasQueuedTaskNotificationsOnOwner(scope *cooperative.Scope) (bool, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return false, e
	}
	for _, item := range root.queue {
		if item.Mode == "task-notification" && item.AgentID == "" {
			return true, nil
		}
	}
	return false, nil
}

func (c *Controller) PruneAgentTaskNotificationsOnOwner(scope *cooperative.Scope, agentID string) error {
	root, e := c.todosController(scope)
	if e != nil {
		return e
	}
	retained := root.queue[:0]
	for _, item := range root.queue {
		if item.Mode == "task-notification" && item.AgentID == agentID {
			continue
		}
		retained = append(retained, item)
	}
	root.queue = retained
	return nil
}
