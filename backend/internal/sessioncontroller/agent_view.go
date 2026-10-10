package sessioncontroller

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (c *Controller) AgentViewSnapshotOnOwner(scope *cooperative.Scope) (protocol.JSONObject, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	out := protocol.NewJSONObject(protocol.JSONField{Name: "viewSelectionMode", Value: quote(root.appState.ViewSelectionMode)})
	if root.appState.ViewingAgentTaskID != "" {
		out.Set("viewingAgentTaskId", quote(root.appState.ViewingAgentTaskID))
	}
	return out, nil
}

func releaseAgentView(data protocol.JSONObject, now float64) protocol.JSONObject {
	out := data.Clone()
	out.Set("retain", raw(false))
	out.Delete("messages")
	out.Set("diskLoaded", raw(false))
	out.Delete("evictAfter")
	switch out.String("status") {
	case "completed", "failed", "killed":
		out.Set("evictAfter", raw(now+30000))
	}
	return out
}
func (c *Controller) EnterAgentViewOnOwner(scope *cooperative.Scope, id string, now float64) error {
	root, e := c.todosController(scope)
	if e != nil {
		return e
	}
	old := root.appState.Tasks[root.appState.ViewingAgentTaskID]
	if root.appState.ViewingAgentTaskID != "" && root.appState.ViewingAgentTaskID != id && old != nil && old.Data.String("type") == "local_agent" && string(old.Data.Get("retain")) == "true" {
		old.Data = releaseAgentView(old.Data, now)
	}
	if task := root.appState.Tasks[id]; task != nil && task.Data.String("type") == "local_agent" {
		data := task.Data.Clone()
		data.Set("retain", raw(true))
		data.Delete("evictAfter")
		task.Data = data
	}
	root.appState.ViewingAgentTaskID = id
	root.appState.ViewSelectionMode = "viewing-agent"
	return nil
}
func (c *Controller) ExitAgentViewOnOwner(scope *cooperative.Scope, now float64) error {
	root, e := c.todosController(scope)
	if e != nil {
		return e
	}
	id := root.appState.ViewingAgentTaskID
	root.appState.ViewingAgentTaskID = ""
	root.appState.ViewSelectionMode = "none"
	if task := root.appState.Tasks[id]; task != nil && task.Data.String("type") == "local_agent" && string(task.Data.Get("retain")) == "true" {
		task.Data = releaseAgentView(task.Data, now)
	}
	return nil
}
func (c *Controller) BootstrapRetainedAgentTranscriptOnOwner(scope *cooperative.Scope, id string, disk []protocol.JSONObject) error {
	return c.UpdateBackgroundTaskOnOwner(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
		if task.String("type") != "local_agent" || string(task.Get("retain")) != "true" || string(task.Get("diskLoaded")) == "true" {
			return task, nil
		}
		live := []protocol.JSONObject{}
		if v := task.Get("messages"); v != nil {
			if e := json.Unmarshal(v, &live); e != nil {
				return task, e
			}
		}
		seen := map[string]bool{}
		for _, row := range live {
			seen[string(row.Get("uuid"))] = true
		}
		merged := []protocol.JSONObject{}
		for _, row := range disk {
			if !seen[string(row.Get("uuid"))] {
				merged = append(merged, row.Clone())
			}
		}
		task.Set("messages", raw(append(merged, live...)))
		task.Set("diskLoaded", raw(true))
		return task, nil
	})
}
