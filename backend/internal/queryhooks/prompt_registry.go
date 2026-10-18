package queryhooks

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func promptIdentity(hook protocol.JSONObject) string {
	return hook.String("prompt") + "\x00" + string(hook.Get("model")) + "\x00" + string(hook.Get("timeout")) + "\x00" + hook.String("if")
}

func (s *Service) RegisterNativePromptHook(scope *cooperative.Scope, sid, event, matcher string, hook protocol.JSONObject, success func(*cooperative.Scope) error) error {
	if s.options.Prompts == nil || !s.HasNativePromptBindings(s.options.Prompts.Owner) || !scope.BelongsTo(s.options.Prompts.Owner) || hook.String("type") != "prompt" || sid == "" {
		return cooperative.ErrRestricted
	}
	store := s.NativeCommandStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.events[sid] == nil {
		store.events[sid] = map[string][]commandMatcher{}
	}
	store.events[sid][event] = append(store.events[sid][event], commandMatcher{matcher, hook.Clone()})
	store.appendLegacyFrontmatterIfTrackedLocked(sid, event, commandMatcher{matcher, hook.Clone()})
	if success != nil {
		if store.promptSuccess == nil {
			store.promptSuccess = map[string]map[string]func(*cooperative.Scope) error{}
		}
		if store.promptSuccess[sid] == nil {
			store.promptSuccess[sid] = map[string]func(*cooperative.Scope) error{}
		}
		store.promptSuccess[sid][event+"\x00"+promptIdentity(hook)] = success
	}
	return nil
}
func (s *Service) RemoveNativePromptHook(scope *cooperative.Scope, sid, event string, hook protocol.JSONObject) error {
	if s.options.Prompts == nil || !s.HasNativePromptBindings(s.options.Prompts.Owner) || !scope.BelongsTo(s.options.Prompts.Owner) {
		return cooperative.ErrRestricted
	}
	store := s.NativeCommandStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	key := promptIdentity(hook)
	if _, tracked := store.frontmatter[sid][event]; tracked {
		logical, err := nativeEventHookKey(hook)
		if err != nil {
			return err
		}
		store.removeEqualFrontmatterLocked(sid, event, logical)
	}
	if events := store.events[sid]; events != nil {
		next := []commandMatcher{}
		for _, row := range events[event] {
			if row.hook.String("type") != "prompt" || promptIdentity(row.hook) != key {
				next = append(next, row)
			}
		}
		events[event] = next
	}
	delete(store.promptSuccess[sid], event+"\x00"+key)
	return nil
}
func (s *Service) promptHookSucceeded(scope *cooperative.Scope, sid, event string, hook protocol.JSONObject) error {
	store := s.NativeCommandStore()
	store.mu.Lock()
	callback := store.promptSuccess[sid][event+"\x00"+promptIdentity(hook)]
	store.mu.Unlock()
	if callback != nil {
		return callback(scope)
	}
	return nil
}

func (s *Service) PromptHooksDisabledOrManagedOnly() (bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.snapshot(); err != nil {
		return false, false, err
	}
	return s.resolver.disabled, s.resolver.managedOnly, nil
}

func promptNumber(hook protocol.JSONObject, key string) float64 {
	var value float64
	_ = json.Unmarshal(hook.Get(key), &value)
	return value
}
