// Package sessionstate holds the process session worker state on its existing
// Owner. Listener and SDK queue dependencies are explicit original callbacks.
package sessionstate

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Options struct {
	OnState    func(*cooperative.Scope, string, *protocol.JSONObject) error
	OnMetadata func(*cooperative.Scope, protocol.JSONObject) error
	// EmitEvents is the live env-truthiness getter, not a constructor snapshot.
	EmitEvents func() bool
	EnqueueSDK func(*cooperative.Scope, protocol.JSONObject) error
}
type State struct {
	owner         *cooperative.Owner
	options       Options
	current       string
	pendingAction bool
}

func New(owner *cooperative.Owner, options Options) (*State, error) {
	if owner == nil || options.EmitEvents == nil {
		return nil, cooperative.ErrRestricted
	}
	return &State{owner: owner, options: options, current: "idle"}, nil
}
func (state *State) Owns(owner *cooperative.Owner) bool {
	return state != nil && owner != nil && state.owner == owner
}
func (state *State) check(scope *cooperative.Scope) error {
	if state == nil || !scope.BelongsTo(state.owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}
func (state *State) Current(scope *cooperative.Scope) (string, error) {
	if e := state.check(scope); e != nil {
		return "", e
	}
	return state.current, nil
}
func metadata(scope *cooperative.Scope, state *State, object protocol.JSONObject) error {
	if state.options.OnMetadata != nil {
		return state.options.OnMetadata(scope, object)
	}
	return nil
}

// Change assigns current state before listeners. A listener error stops the
// remaining Source body, preserving already committed state and pending flag.
func (state *State) Change(scope *cooperative.Scope, next string, details *protocol.JSONObject) error {
	if e := state.check(scope); e != nil {
		return e
	}
	if next != "idle" && next != "running" && next != "requires_action" {
		return cooperative.ErrRestricted
	}
	state.current = next
	if state.options.OnState != nil {
		if e := state.options.OnState(scope, next, details); e != nil {
			return e
		}
	}
	if next == "requires_action" && details != nil {
		state.pendingAction = true
		b, e := details.MarshalJSON()
		if e != nil {
			return e
		}
		if e = metadata(scope, state, protocol.NewJSONObject(protocol.JSONField{Name: "pending_action", Value: b})); e != nil {
			return e
		}
	} else if state.pendingAction {
		state.pendingAction = false
		if e := metadata(scope, state, protocol.NewJSONObject(protocol.JSONField{Name: "pending_action", Value: []byte("null")})); e != nil {
			return e
		}
	}
	if next == "idle" {
		if e := metadata(scope, state, protocol.NewJSONObject(protocol.JSONField{Name: "task_summary", Value: []byte("null")})); e != nil {
			return e
		}
	}
	if state.options.EmitEvents() {
		if state.options.EnqueueSDK == nil {
			return cooperative.ErrRestricted
		}
		event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"system"`)}, protocol.JSONField{Name: "subtype", Value: []byte(`"session_state_changed"`)})
		switch next {
		case "idle":
			event.Set("state", []byte(`"idle"`))
		case "running":
			event.Set("state", []byte(`"running"`))
		case "requires_action":
			event.Set("state", []byte(`"requires_action"`))
		}
		return state.options.EnqueueSDK(scope, event)
	}
	return nil
}
