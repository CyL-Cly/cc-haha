// Package pluginhooks owns the shared metadata registry swapped by explicit
// reload and pruned by plugin management. Execution remains a Harness consumer.
package pluginhooks

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sync"
)

var Events = []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionDenied", "Notification", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop", "StopFailure", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "PermissionRequest", "Setup", "TeammateIdle", "TaskCreated", "TaskCompleted", "Elicitation", "ElicitationResult", "ConfigChange", "WorktreeCreate", "WorktreeRemove", "InstructionsLoaded", "CwdChanged", "FileChanged"}

type Registry struct {
	mu         sync.Mutex
	loaded     bool
	generation uint64
	registered *protocol.JSONObject
}

func New() *Registry            { return &Registry{} }
func raw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func object(v json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(v)
	return out
}
func values(v json.RawMessage) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	_ = json.Unmarshal(v, &out)
	return out
}
func (r *Registry) Clear() { r.mu.Lock(); r.loaded = false; r.generation++; r.mu.Unlock() }
func (r *Registry) Snapshot() *protocol.JSONObject {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.registered == nil {
		return nil
	}
	copy := r.registered.Clone()
	return &copy
}
func (r *Registry) Register(callbacks protocol.JSONObject) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.register(callbacks)
}
func (r *Registry) register(incoming protocol.JSONObject) {
	if r.registered == nil {
		current := protocol.NewJSONObject()
		r.registered = &current
	}
	for _, p := range incoming.OwnProperties() {
		name := ""
		_ = json.Unmarshal(p.NameJSON, &name)
		joined := append(values(r.registered.Get(name)), values(p.Value)...)
		r.registered.Set(name, raw(joined))
	}
}
func (r *Registry) clearPluginHooks() {
	if r.registered == nil {
		return
	}
	filtered := protocol.NewJSONObject()
	for _, p := range r.registered.OwnProperties() {
		name := ""
		_ = json.Unmarshal(p.NameJSON, &name)
		survivors := []protocol.JSONObject{}
		for _, matcher := range values(p.Value) {
			if matcher.Get("pluginRoot") == nil {
				survivors = append(survivors, matcher)
			}
		}
		if len(survivors) > 0 {
			filtered.Set(name, raw(survivors))
		}
	}
	if len(filtered.OwnProperties()) == 0 {
		r.registered = nil
	} else {
		r.registered = &filtered
	}
}
func (r *Registry) LoadPlugins(plugins []pluginregistry.Loaded) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return
	}
	all := protocol.NewJSONObject()
	for _, event := range Events {
		all.Set(event, raw([]protocol.JSONObject{}))
	}
	for _, p := range plugins {
		if !p.Enabled || p.HooksConfig == nil {
			continue
		}
		for _, event := range Events {
			matchers := values(p.HooksConfig.Get(event))
			out := values(all.Get(event))
			for _, m := range matchers {
				if len(values(m.Get("hooks"))) == 0 {
					continue
				}
				converted := protocol.NewJSONObject(protocol.JSONField{Name: "hooks", Value: m.Get("hooks")}, protocol.JSONField{Name: "pluginRoot", Value: raw(p.Path)}, protocol.JSONField{Name: "pluginName", Value: raw(p.Name)}, protocol.JSONField{Name: "pluginId", Value: raw(p.Source)})
				converted.Set("matcher", m.Get("matcher"))
				out = append(out, converted)
			}
			all.Set(event, raw(out))
		}
	}
	r.clearPluginHooks()
	r.register(all)
	r.loaded = true
}
func (r *Registry) Prune(enabled []pluginregistry.Loaded) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.registered == nil {
		return
	}
	roots := map[string]bool{}
	for _, p := range enabled {
		if p.Enabled {
			roots[p.Path] = true
		}
	}
	survivors := protocol.NewJSONObject()
	for _, property := range r.registered.OwnProperties() {
		name := ""
		_ = json.Unmarshal(property.NameJSON, &name)
		kept := []protocol.JSONObject{}
		for _, m := range values(property.Value) {
			if m.Get("pluginRoot") != nil && roots[m.String("pluginRoot")] {
				kept = append(kept, m)
			}
		}
		if len(kept) > 0 {
			survivors.Set(name, raw(kept))
		}
	}
	r.clearPluginHooks()
	r.register(survivors)
}
