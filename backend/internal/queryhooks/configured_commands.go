package queryhooks

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Selection is performed on the captured original settings object, followed
// by session hooks. Pattern filtering and last-value command deduplication
// run against the actual native HookInput in the executor.
func (s *Service) configuredNativeCommands(event, sid string) ([]commandMatcher, error) {
	if s.options.Commands == nil {
		return s.nativeCommandMatchers(event, sid), nil
	}
	snapshot, e := s.snapshot()
	if e != nil {
		return nil, e
	}
	rows := []commandMatcher{}
	if raw := snapshot.Get(event); raw != nil {
		var matchers []protocol.JSONObject
		if json.Unmarshal(raw, &matchers) != nil {
			return nil, unsupported("mutated hook snapshot")
		}
		for _, matcher := range matchers {
			var hooks []protocol.JSONObject
			if json.Unmarshal(matcher.Get("hooks"), &hooks) != nil {
				return nil, unsupported("mutated hook matcher")
			}
			for _, hook := range hooks {
				rows = append(rows, commandMatcher{matcher.String("matcher"), hook.Clone()})
			}
		}
	}
	if !s.resolver.managedOnly {
		rows = append(rows, s.nativeCommandMatchers(event, sid)...)
	}
	// SDK callbacks remain eligible under managed-only customization. They
	// are separate function identities and are never command-key deduplicated.
	if event != "WorktreeCreate" && event != "WorktreeRemove" {
		rows = append(rows, s.sdkCommandMatchers(event)...)
	}
	return rows, nil
}
func (s *Service) activeCallbacks(event, sid string) bool {
	for _, matcher := range s.registry.registered[event] {
		if len(matcher.Hooks) > 0 {
			return true
		}
	}
	if !s.resolver.managedOnly {
		if store := s.registry.sessions[sid]; store != nil {
			for _, matcher := range store.events[event] {
				if len(matcher.Hooks) > 0 {
					return true
				}
			}
		}
	}
	return false
}

func selectNativeStopCommands(event string, input protocol.JSONObject, rows []commandMatcher, promptBound ...bool) ([]protocol.JSONObject, error) {
	selected := []protocol.JSONObject{}
	indices := map[string]int{}
	for _, row := range rows {
		if event != "Stop" && input.String("agent_type") != "" && !nativeCommandMatch(input.String("agent_type"), row.matcher) {
			continue
		}
		hook := row.hook
		if hook.String("type") == "callback" {
			selected = append(selected, hook.Clone())
			continue
		}
		key, e := nativeEventHookKey(hook)
		if e != nil {
			return nil, e
		}
		if i, ok := indices[key]; ok {
			selected[i] = hook.Clone()
		} else {
			indices[key] = len(selected)
			selected = append(selected, hook.Clone())
		}
	}
	grouped := make([]protocol.JSONObject, 0, len(selected))
	for _, kind := range []string{"command", "prompt", "agent", "http", "callback"} {
		for _, hook := range selected {
			if hook.String("type") != kind {
				continue
			}
			// Source cannot evaluate permission predicates for a non-tool event.
			if hook.String("if") != "" {
				continue
			}
			prompt := kind == "prompt" && len(promptBound) >= 1 && promptBound[0]
			httpBound := kind == "http" && len(promptBound) >= 2 && promptBound[1]
			agentBound := kind == "agent" && len(promptBound) >= 3 && promptBound[2]
			callbackBound := kind == "callback" && len(promptBound) > 4 && promptBound[4]
			if kind != "command" && !prompt && !httpBound && !agentBound && !callbackBound {
				return nil, unsupported(event + " hook kind " + hook.String("type"))
			}
			if kind == "command" && (string(hook.Get("async")) == "true" || string(hook.Get("asyncRewake")) == "true") && !(len(promptBound) > 3 && promptBound[3]) {
				return nil, unsupported("command hook asynchronous task lifecycle")
			}
			grouped = append(grouped, hook)
		}
	}
	return grouped, nil
}
