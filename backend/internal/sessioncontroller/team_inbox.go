package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The leader inbox is transient AppState, never a second transcript or a worker
// queue. IDs belong to the existing session factory's UUID producer.
func (c *Controller) QueueTeamInboxOnOwner(scope *cooperative.Scope, messages []protocol.JSONObject) error {
	if _, err := c.todosController(scope); err != nil {
		return err
	}
	if !c.paneInboxRole() && (c.options.TeamContext != nil || c.options.InProcessTeammateContext != nil || c.leaderTeamName == "") {
		return cooperative.ErrRestricted
	}
	for _, message := range messages {
		row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quote(c.options.UUID())})
		for _, name := range []string{"from", "text", "timestamp", "color", "summary"} {
			if message.Get(name) != nil {
				row.Set(name, message.Get(name))
			}
		}
		row.Set("status", quote("pending"))
		c.appState.Inbox = append(c.appState.Inbox, row)
	}
	return nil
}

func (c *Controller) PendingTeamInboxOnOwner(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if _, err := c.todosController(scope); err != nil {
		return nil, err
	}
	if c.appState.ViewingAgentTaskID != "" {
		return []protocol.JSONObject{}, nil
	}
	return c.LeaderPendingTeamInboxOnOwner(scope)
}

// Idle leader turns do not borrow the viewed teammate's attachment filter.
func (c *Controller) LeaderPendingTeamInboxOnOwner(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if _, err := c.todosController(scope); err != nil {
		return nil, err
	}
	if !c.paneInboxRole() && (c.options.TeamContext != nil || c.options.InProcessTeammateContext != nil) {
		return []protocol.JSONObject{}, nil
	}
	rows := []protocol.JSONObject{}
	for _, row := range c.appState.Inbox {
		if row.String("status") == "pending" {
			rows = append(rows, row.Clone())
		}
	}
	return rows, nil
}

func (c *Controller) ProcessTeamInboxOnOwner(scope *cooperative.Scope, messages []protocol.JSONObject) error {
	if _, err := c.todosController(scope); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, row := range messages {
		ids[string(row.Get("id"))] = true
	}
	for i, row := range c.appState.Inbox {
		if ids[string(row.Get("id"))] {
			row = row.Clone()
			row.Set("status", quote("processed"))
			c.appState.Inbox[i] = row
		}
	}
	return nil
}

func (c *Controller) FinishTeamInboxOnOwner(scope *cooperative.Scope, submitted []protocol.JSONObject) error {
	if _, err := c.todosController(scope); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, row := range submitted {
		ids[string(row.Get("id"))] = true
	}
	rows := []protocol.JSONObject{}
	for _, row := range c.appState.Inbox {
		if row.String("status") != "processed" && !ids[string(row.Get("id"))] {
			rows = append(rows, row)
		}
	}
	c.appState.Inbox = rows
	return nil
}

func (c *Controller) HasLeaderQueuedUserInputOnOwner(scope *cooperative.Scope) (bool, error) {
	if _, err := c.todosController(scope); err != nil {
		return false, err
	}
	for _, item := range c.queue {
		if item.AgentID == "" && (item.Mode == "prompt" || item.Mode == "bash") {
			return true, nil
		}
	}
	return false, nil
}

func (c *Controller) paneInboxRole() bool {
	return c.options.TeamContext != nil && c.options.TeamContext.identity != nil && c.options.InProcessTeammateContext == nil
}
