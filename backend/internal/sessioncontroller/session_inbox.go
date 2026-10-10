package sessioncontroller

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioninbox"
)

func (c *Controller) BindSessionInboxOnOwner(scope *cooperative.Scope, receipt func(protocol.JSONObject) error) error {
	if c == nil || c.options.Bootstrap == nil || !c.active || c.sessionInbox != nil {
		return cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return e
	}
	box, e := sessioninbox.New(func(value protocol.JSONObject) error {
		if c.inboxScope == nil {
			return cooperative.ErrRestricted
		}
		return c.enqueueOwned(c.inboxScope, command{Content: value.Get("value"), UUID: value.Get("uuid"), Priority: value.String("priority"), Meta: string(value.Get("isMeta")) == "true", Mode: value.String("mode"), Origin: value.Get("origin"), SkipSlash: string(value.Get("skipSlashCommands")) == "true"})
	}, receipt, c.History())
	if e != nil {
		return e
	}
	c.sessionInbox = box
	return nil
}
func (c *Controller) inboxPending(uuid json.RawMessage) bool {
	return c.sessionInbox != nil && c.sessionInbox.Pending(text(uuid))
}
func (c *Controller) AcceptSessionMessageOnOwner(scope *cooperative.Scope, input protocol.JSONObject, persisted bool) (protocol.JSONObject, error) {
	if c == nil || c.sessionInbox == nil || c.options.Bootstrap == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return protocol.JSONObject{}, e
	}
	c.inboxScope = scope
	defer func() { c.inboxScope = nil }()
	return c.sessionInbox.Accept(raw(input), persisted)
}
func (c *Controller) consumeInboxOnOwner(scope *cooperative.Scope, uuid json.RawMessage) error {
	if c.sessionInbox == nil {
		return nil
	}
	if scope == nil || c.options.Bootstrap == nil {
		return cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return e
	}
	return c.sessionInbox.Consume(text(uuid))
}
func (c *Controller) SessionMessagePendingOnOwner(scope *cooperative.Scope, uuid string) (bool, error) {
	if c == nil || c.options.Bootstrap == nil {
		return false, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return false, e
	}
	return c.sessionInbox != nil && c.sessionInbox.Pending(uuid), nil
}

// Consumption follows accepted input's canonical Record and Flush, rather
// than queue extraction. The disabled-persistence caller still consumes.
func (c *Controller) ConsumeSessionMessageOnOwner(scope *cooperative.Scope, uuid string) error {
	if c == nil || c.options.Bootstrap == nil {
		return cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return e
	}
	return c.consumeInboxOnOwner(scope, quote(uuid))
}

func (c *Controller) ReplayUserMessagesOnOwner(scope *cooperative.Scope) (bool, error) {
	if c == nil || c.options.Bootstrap == nil {
		return false, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return false, e
	}
	return c.options.ReplayUserMessages, nil
}

func (c *Controller) SessionMessagePersistedOnOwner(scope *cooperative.Scope, uuid string) (bool, error) {
	if c == nil || c.options.Bootstrap == nil || c.writer.async == nil {
		return false, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return false, e
	}
	if c.inboxPending(quote(uuid)) {
		return false, nil
	}
	future, e := c.writer.async.SessionUUIDs(scope, c.options.ResumeOptions.Loader, c.SessionID())
	if e != nil {
		return false, e
	}
	set, e := future.Await(scope)
	if e != nil {
		return false, e
	}
	return set.OwnerHas(scope, quote(uuid))
}
func (c *Controller) CancelSessionInboxOnOwner(scope *cooperative.Scope) (int, error) {
	if c == nil || c.sessionInbox == nil || c.options.Bootstrap == nil {
		return 0, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return 0, e
	}
	return c.sessionInbox.CancelQueued(func(predicate func(protocol.JSONObject) bool) ([]protocol.JSONObject, error) {
		removed, retained := []protocol.JSONObject{}, []command{}
		for _, item := range c.queue {
			value := protocol.NewJSONObject(protocol.JSONField{Name: "uuid", Value: item.UUID})
			if predicate(value) {
				removed = append(removed, value)
				if e := c.logQueueOwned(scope, "remove", nil); e != nil {
					return nil, e
				}
			} else {
				retained = append(retained, item)
			}
		}
		c.queue = retained
		return removed, nil
	})
}
func (c *Controller) DisposeSessionInboxOnOwner(scope *cooperative.Scope) error {
	if c.options.Bootstrap == nil {
		return cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return e
	}
	if c.sessionInbox != nil {
		c.sessionInbox.Dispose()
	}
	return nil
}

// Channel prompts are consumed at the same next-priority post-tool boundary
// as the original queued commands. Their UUID also fences ASK batching.
func (c *Controller) TakeSessionInboxAttachmentsOnOwner(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if c == nil || c.options.Bootstrap == nil || !c.active {
		return nil, cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return nil, e
	}
	out := []protocol.JSONObject{}
	retained := []command{}
	for _, item := range c.queue {
		if !c.inboxPending(item.UUID) || item.Priority == "later" {
			retained = append(retained, item)
			continue
		}
		payload := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("queued_command")}, protocol.JSONField{Name: "prompt", Value: item.Content}, protocol.JSONField{Name: "source_uuid", Value: item.UUID}, protocol.JSONField{Name: "imagePasteIds", Value: raw([]string{})}, protocol.JSONField{Name: "commandMode", Value: quote(item.Mode)}, protocol.JSONField{Name: "isMeta", Value: raw(item.Meta)}, protocol.JSONField{Name: "origin", Value: item.Origin})
		out = append(out, payload)
		if e := c.logQueueOwned(scope, "remove", nil); e != nil {
			return nil, e
		}
	}
	c.queue = retained
	return out, nil
}

func (c *Controller) TakeNativeQueuedAttachmentsOnOwner(scope *cooperative.Scope, agentID string) ([]protocol.JSONObject, error) {
	root, e := c.todosController(scope)
	if e != nil {
		return nil, e
	}
	out := []protocol.JSONObject{}
	retained := []command{}
	for _, item := range root.queue {
		task := item.Mode == "task-notification" && item.AgentID == agentID && item.Priority != "later"
		inbox := agentID == "" && root.inboxPending(item.UUID) && item.Priority != "later"
		if !task && !inbox {
			retained = append(retained, item)
			continue
		}
		payload := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("queued_command")}, protocol.JSONField{Name: "prompt", Value: item.Content}, protocol.JSONField{Name: "source_uuid", Value: item.UUID}, protocol.JSONField{Name: "imagePasteIds", Value: raw([]string{})}, protocol.JSONField{Name: "commandMode", Value: quote(item.Mode)}, protocol.JSONField{Name: "isMeta", Value: raw(item.Meta)})
		if item.Origin != nil {
			payload.Set("origin", item.Origin)
		}
		out = append(out, payload)
		if e = root.logQueueOwned(scope, "remove", nil); e != nil {
			return nil, e
		}
	}
	root.queue = retained
	return out, nil
}
