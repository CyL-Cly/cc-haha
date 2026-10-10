package sessioncontroller

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// TodoWrite uses the process AppState with a session/agent key. A sidechain
// shares that AppState, while its original transcript and read cache stay local.
func (c *Controller) todosController(scope *cooperative.Scope) (*Controller, error) {
	if c == nil || c.options.Bootstrap == nil || !c.active {
		return nil, cooperative.ErrRestricted
	}
	owner := c.options.Bootstrap.Owner()
	seen := map[*Controller]bool{}
	for current := c; current != nil; current = current.options.InheritedPermissions {
		if seen[current] || !current.BootstrapOwns(owner) {
			return nil, cooperative.ErrRestricted
		}
		seen[current] = true
		if err := current.options.Bootstrap.CheckScope(scope); err != nil {
			return nil, err
		}
		if current.options.InheritedPermissions == nil {
			return current, nil
		}
	}
	return nil, cooperative.ErrRestricted
}

func (c *Controller) todoKey() string {
	if c.options.WriterOptions.AgentID != "" {
		return c.options.WriterOptions.AgentID
	}
	return c.SessionID()
}

func (c *Controller) GetTodosOnOwner(scope *cooperative.Scope) (json.RawMessage, error) {
	root, err := c.todosController(scope)
	if err != nil {
		return nil, err
	}
	value := root.appState.Todos.Get(c.todoKey())
	if value == nil || string(value) == "null" {
		return json.RawMessage("[]"), nil
	}
	return append(json.RawMessage(nil), value...), nil
}

func (c *Controller) SetTodosOnOwner(scope *cooperative.Scope, value json.RawMessage) error {
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(value, &items); err != nil || items == nil {
		return capability("TodoWrite AppState requires the original todo array")
	}
	root.appState.Todos.Set(c.todoKey(), append(json.RawMessage(nil), value...))
	return nil
}
