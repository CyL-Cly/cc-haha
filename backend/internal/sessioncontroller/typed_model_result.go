package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// AppendTypedModelResult is processSlashCommand's actual local JSX completion.
// Default display constructs the two users before the outer caveat. System
// display constructs only UI records. Neither branch enqueues another ASK.
func (c *Controller) AppendTypedModelResult(scope *cooperative.Scope, args, notice string, system bool) ([]protocol.JSONObject, error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || !c.active || c.options.WriterOptions.AgentID != "" {
		return nil, cooperative.ErrRestricted
	}
	if err := c.options.Bootstrap.CheckScope(scope); err != nil {
		return nil, err
	}
	tagged := "<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>" + args + "</command-args>"
	stdout := "<local-command-stdout>" + notice + "</local-command-stdout>"
	var rows []protocol.JSONObject
	if system {
		rows = []protocol.JSONObject{c.options.Factory.CreateLocalCommandInput(tagged), c.options.Factory.CreateLocalCommandInput(stdout)}
	} else {
		input := messages.OrderedUser(c.options.Factory.CreateUser(storage.Object{"content": quote(tagged)}))
		output := messages.OrderedUser(c.options.Factory.CreateUser(storage.Object{"content": quote(stdout)}))
		caveat := c.options.Factory.CreateLocalCaveat()
		rows = []protocol.JSONObject{caveat, input, output}
	}
	for _, row := range rows {
		message := row
		c.messages = append(c.messages, &message)
	}
	return rows, nil
}
