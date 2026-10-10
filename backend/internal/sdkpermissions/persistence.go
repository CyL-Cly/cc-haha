package sdkpermissions

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type PersistenceOptions struct {
	ValidatedSource  func(string) (*protocol.JSONObject, error)
	EditingSource    func(string) (*protocol.JSONObject, error)
	UpdateSource     func(string, protocol.JSONObject) error
	ManagedRulesOnly func() (bool, error)
	LogError         func(error)
}
type Persistence struct{ options PersistenceOptions }

func NewPersistence(o PersistenceOptions) (*Persistence, error) {
	if o.ValidatedSource == nil || o.EditingSource == nil || o.UpdateSource == nil || o.ManagedRulesOnly == nil || o.LogError == nil {
		return nil, errors.New("permission persistence requires actual settings/editing/writer/policy/error producers")
	}
	return &Persistence{o}, nil
}
func persistRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func persistPermissions(settings *protocol.JSONObject) protocol.JSONObject {
	out := protocol.NewJSONObject()
	if settings != nil {
		_ = json.Unmarshal(settings.Get("permissions"), &out)
	}
	if !out.IsObject() {
		out = protocol.NewJSONObject()
	}
	return out
}
func (p *Persistence) Persist(update protocol.JSONObject) error {
	if !supportsPermissionPersistence(update.String("destination")) {
		return nil
	}
	if e := p.persist(update); e != nil {
		p.options.LogError(e)
	}
	// Source's disk-writing failures are logged; an already granted tool call
	// keeps its live permission decision. Administrative admission lives above.
	return nil
}
func (p *Persistence) persist(update protocol.JSONObject) error {
	source := update.String("destination")
	kind := update.String("type")
	if kind == "addRules" {
		managed, e := p.options.ManagedRulesOnly()
		if e != nil {
			return e
		}
		if managed {
			return nil
		}
		var rules []protocol.JSONObject
		_ = json.Unmarshal(update.Get("rules"), &rules)
		if len(rules) == 0 {
			return nil
		}
	}
	settings, e := p.options.ValidatedSource(source)
	if e != nil {
		return e
	}
	if kind == "addRules" && settings == nil {
		settings, e = p.options.EditingSource(source)
		if e != nil {
			return e
		}
	}
	permissions := persistPermissions(settings)
	patch := protocol.NewJSONObject()
	behavior := update.String("behavior")
	switch kind {
	case "addRules", "removeRules", "replaceRules":
		incoming, e := permissionUpdateRules(update)
		if e != nil {
			return e
		}
		var current []json.RawMessage
		value := permissions.Get(behavior)
		if value != nil && string(value) != "null" {
			if e = json.Unmarshal(value, &current); e != nil {
				return e
			}
		}
		result := []json.RawMessage{}
		if kind == "replaceRules" {
			result = incoming
		} else {
			existing := map[string]bool{}
			for _, rule := range current {
				normalized, e := normalizePersistedRule(rule)
				if e != nil {
					return e
				}
				existing[rawStringIdentity(normalized)] = true
			}
			if kind == "addRules" {
				result = append(result, current...)
				added := 0
				for _, rule := range incoming {
					if !existing[rawStringIdentity(rule)] {
						result = append(result, rule)
						added++
					}
				}
				if added == 0 {
					return nil
				}
			} else {
				remove := map[string]bool{}
				for _, rule := range incoming {
					remove[rawStringIdentity(rule)] = true
				}
				for _, rule := range current {
					normalized, _ := normalizePersistedRule(rule)
					if !remove[rawStringIdentity(normalized)] {
						result = append(result, rule)
					}
				}
			}
		}
		permissions.Set(behavior, persistRaw(result))
		if kind == "addRules" {
			if settings != nil {
				patch = settings.Clone()
			}
			patch.Set("permissions", persistRaw(permissions))
		} else {
			patch.Set("permissions", persistRaw(protocol.NewJSONObject(protocol.JSONField{Name: behavior, Value: persistRaw(result)})))
		}
	case "addDirectories", "removeDirectories":
		var current, incoming []json.RawMessage
		if value := permissions.Get("additionalDirectories"); value != nil && string(value) != "null" {
			if e = json.Unmarshal(value, &current); e != nil {
				return e
			}
		}
		if e = json.Unmarshal(update.Get("directories"), &incoming); e != nil {
			return e
		}
		result := []json.RawMessage{}
		if kind == "addDirectories" {
			existing := map[string]bool{}
			for _, dir := range current {
				existing[rawStringIdentity(dir)] = true
			}
			result = append(result, current...)
			added := 0
			for _, dir := range incoming {
				if !existing[rawStringIdentity(dir)] {
					result = append(result, dir)
					added++
				}
			}
			if added == 0 {
				return nil
			}
		} else {
			remove := map[string]bool{}
			for _, dir := range incoming {
				remove[rawStringIdentity(dir)] = true
			}
			for _, dir := range current {
				if !remove[rawStringIdentity(dir)] {
					result = append(result, dir)
				}
			}
		}
		patch.Set("permissions", persistRaw(protocol.NewJSONObject(protocol.JSONField{Name: "additionalDirectories", Value: persistRaw(result)})))
	case "setMode":
		patch.Set("permissions", persistRaw(protocol.NewJSONObject(protocol.JSONField{Name: "defaultMode", Value: update.Get("mode")})))
	default:
		return nil
	}
	return p.options.UpdateSource(source, patch)
}

// normalizePersistedRule is the Source parse→serialize roundtrip. Incoming
// update values are serialized directly; only stored legacy aliases/wildcards
// are normalized for duplicate/removal comparisons.
func normalizePersistedRule(raw json.RawMessage) (json.RawMessage, error) {
	rule, e := ParsePermissionRuleValue(raw)
	if e != nil {
		return nil, e
	}

	values, e := permissionUpdateRules(protocol.NewJSONObject(protocol.JSONField{Name: "rules", Value: persistRaw([]protocol.JSONObject{rule})}))
	if e != nil {
		return nil, e
	}
	return values[0], nil
}
