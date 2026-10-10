package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// InstallAgentPrelude is the fresh agent initialMessages stage. It never marks
// a conversation resumed or changes its identity, tools, model or permissions.
// The ordinary accepted ASK records these original envelopes with its user.
func (c *Controller) InstallAgentPrelude(scope *cooperative.Scope, rows []protocol.JSONObject) error {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || !c.active || c.options.WriterOptions.AgentID == "" || len(c.messages) != 0 || len(c.queue) != 0 {
		return cooperative.ErrRestricted
	}
	for _, row := range rows {
		if row.String("type") != "user" || row.String("uuid") == "" || string(row.Get("isMeta")) != "true" {
			return cooperative.ErrRestricted
		}
	}
	for _, row := range rows {
		copy := row.Clone()
		c.messages = append(c.messages, &copy)
	}
	return nil
}
