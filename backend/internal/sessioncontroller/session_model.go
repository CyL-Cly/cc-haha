package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// InjectSessionModelBreadcrumbs appends Source's local SDK /model messages to
// the active main history; it does not enqueue a user turn or write settings.
func (c *Controller) InjectSessionModelBreadcrumbs(scope *cooperative.Scope, requested, display string) ([]protocol.JSONObject, error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || !c.active || c.options.WriterOptions.AgentID != "" {
		return nil, cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return nil, err
	}
	rows := []protocol.JSONObject{c.options.Factory.CreateLocalCaveat(), messages.OrderedUser(c.options.Factory.CreateUser(storage.Object{"content": raw("<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>" + requested + "</command-args>")})), messages.OrderedUser(c.options.Factory.CreateUser(storage.Object{"content": raw("<local-command-stdout>Set model to " + display + "</local-command-stdout>")}))}
	for _, row := range rows {
		message := row
		c.messages = append(c.messages, &message)
	}
	return rows, nil
}
