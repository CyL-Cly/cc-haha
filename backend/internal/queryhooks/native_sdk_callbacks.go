package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// CallbackBindings borrows the live SDK control transport on its parent Owner.
// Descriptors identify callbacks; neither descriptors nor the store contain a
// pre-resolved callback output or an independent hook executor.
type CallbackBindings struct {
	Owner *cooperative.Owner
	Run   func(context.Context, Context, protocol.JSONObject, protocol.JSONObject) (protocol.JSONObject, error)
	Store *CallbackStore
}
type CallbackStore struct {
	mu     sync.Mutex
	events map[string][]protocol.JSONObject
}

func (s *Service) BindNativeSDKCallbacks(scope *cooperative.Scope, b *CallbackBindings) error {
	if b == nil || b.Owner == nil || b.Run == nil || !scope.BelongsTo(b.Owner) || !s.HasNativeCommandBindings(b.Owner) || s.options.Profile != ResolvedSettingsProfile {
		return cooperative.ErrRestricted
	}
	if b.Store == nil {
		b.Store = &CallbackStore{events: map[string][]protocol.JSONObject{}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.Callbacks = b
	return nil
}
func (s *Service) HasNativeCallbackBindings(owner *cooperative.Owner) bool {
	return s != nil && s.options.Callbacks != nil && s.options.Callbacks.Owner == owner && s.options.Callbacks.Run != nil
}

// Only events with actual native consumers may be registered here. Worktree
// matching has no matchQuery; ordinary tool and Stop events use the same store.
func (s *Service) RegisterSDKWorktreeCallbacks(scope *cooperative.Scope, hooks protocol.JSONObject) error {
	if s.options.Callbacks == nil || !scope.BelongsTo(s.options.Callbacks.Owner) {
		return cooperative.ErrRestricted
	}
	prepared := map[string][]protocol.JSONObject{}
	for _, property := range hooks.OwnProperties() {
		var event string
		_ = json.Unmarshal(property.NameJSON, &event)
		if event != "WorktreeCreate" && event != "WorktreeRemove" && event != "PreToolUse" && event != "PostToolUse" && event != "Stop" && event != "PermissionRequest" {
			return unsupported("SDK callback event " + event)
		}
		var matchers []protocol.JSONObject
		if json.Unmarshal(property.Value, &matchers) != nil {
			return errors.New("SDK callback matchers must be an array")
		}
		for _, matcher := range matchers {
			var ids []json.RawMessage
			if json.Unmarshal(matcher.Get("hookCallbackIds"), &ids) != nil {
				return errors.New("SDK hookCallbackIds must be an array")
			}
			registered := protocol.NewJSONObject()
			if matcher.Get("matcher") != nil {
				registered.Set("matcher", matcher.Get("matcher"))
			}
			rows := []protocol.JSONObject{}
			for _, id := range ids {
				row := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("callback")}, protocol.JSONField{Name: "callback_id", Value: id})
				if matcher.Get("timeout") != nil {
					row.Set("timeout", matcher.Get("timeout"))
				}
				rows = append(rows, row)
			}
			registered.Set("hooks", commandJSON(rows))
			prepared[event] = append(prepared[event], registered)
		}
	}
	store := s.options.Callbacks.Store
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.events == nil {
		store.events = map[string][]protocol.JSONObject{}
	}
	for event, rows := range prepared {
		store.events[event] = append(store.events[event], rows...)
	}
	return nil
}

func (s *Service) sdkCallbackMatchers(event string) []protocol.JSONObject {
	b := s.options.Callbacks
	if b == nil || b.Store == nil {
		return nil
	}
	b.Store.mu.Lock()
	defer b.Store.mu.Unlock()
	rows := []protocol.JSONObject{}
	for _, row := range b.Store.events[event] {
		rows = append(rows, row.Clone())
	}
	return rows
}
func (s *Service) clearSDKCallbackMatchers() {
	b := s.options.Callbacks
	if b == nil || b.Store == nil {
		return
	}
	b.Store.mu.Lock()
	defer b.Store.mu.Unlock()
	b.Store.events = map[string][]protocol.JSONObject{}
}

// NativeSDKCallbackBindings borrows the live descriptor store/transport only
// from its actual parent Owner. Consumers still execute on their own Owner and
// explicitly bridge an immutable callback packet back to that parent.
func (s *Service) NativeSDKCallbackBindings(owner *cooperative.Owner) *CallbackBindings {
	if !s.HasNativeCallbackBindings(owner) {
		return nil
	}
	return s.options.Callbacks
}
