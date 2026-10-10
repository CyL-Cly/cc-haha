package sdkpermissions

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PermissionPromptToolResultSchema ignores an entire malformed update batch.
// Preserve quoted UTF-16 values instead of roundtripping rule content through
// replacement-decoded Go strings.
func ParsePermissionUpdates(data json.RawMessage) []protocol.JSONObject {
	var rows []protocol.JSONObject
	if json.Unmarshal(data, &rows) != nil {
		return nil
	}
	out := []protocol.JSONObject{}
	stringField := func(o protocol.JSONObject, key string) bool {
		_, e := protocol.DecodeJavaScriptJSONString(o.Get(key))
		return e == nil
	}
	for _, row := range rows {
		if !row.IsObject() || !stringField(row, "type") || !stringField(row, "destination") {
			return nil
		}
		switch row.String("destination") {
		case "userSettings", "projectSettings", "localSettings", "session", "cliArg":
		default:
			return nil
		}
		next := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: row.Get("type")})
		switch row.String("type") {
		case "setMode":
			switch row.String("mode") {
			case "acceptEdits", "bypassPermissions", "default", "dontAsk", "plan", "auto":
			default:
				return nil
			}
			next.Set("mode", row.Get("mode"))
		case "addRules", "replaceRules", "removeRules":
			switch row.String("behavior") {
			case "allow", "deny", "ask":
			default:
				return nil
			}
			var rules []protocol.JSONObject
			if json.Unmarshal(row.Get("rules"), &rules) != nil || rules == nil {
				return nil
			}
			clean := []protocol.JSONObject{}
			for _, rule := range rules {
				if !rule.IsObject() || !stringField(rule, "toolName") {
					return nil
				}
				value := protocol.NewJSONObject(protocol.JSONField{Name: "toolName", Value: rule.Get("toolName")})
				if content := rule.Get("ruleContent"); content != nil {
					if !stringField(rule, "ruleContent") {
						return nil
					}
					value.Set("ruleContent", content)
				}
				clean = append(clean, value)
			}
			raw, _ := json.Marshal(clean)
			next.Set("rules", raw)
			next.Set("behavior", row.Get("behavior"))
		case "addDirectories", "removeDirectories":
			var dirs []json.RawMessage
			if json.Unmarshal(row.Get("directories"), &dirs) != nil || dirs == nil {
				return nil
			}
			for _, dir := range dirs {
				if _, e := protocol.DecodeJavaScriptJSONString(dir); e != nil {
					return nil
				}
			}
			next.Set("directories", row.Get("directories"))
		default:
			return nil
		}
		next.Set("destination", row.Get("destination"))
		out = append(out, next)
	}
	return out
}
func permissionUpdateRules(row protocol.JSONObject) ([]json.RawMessage, error) {
	var rules []protocol.JSONObject
	if e := json.Unmarshal(row.Get("rules"), &rules); e != nil {
		return nil, e
	}
	values := []json.RawMessage{}
	for _, rule := range rules {
		name, e := protocol.DecodeJavaScriptJSONString(rule.Get("toolName"))
		if e != nil {
			return nil, e
		}
		if content := rule.Get("ruleContent"); content != nil {
			units, e := protocol.DecodeJavaScriptJSONString(content)
			if e != nil {
				return nil, e
			}
			if len(units) > 0 {
				name = append(name, '(')
				for _, unit := range units {
					if unit == '\\' || unit == '(' || unit == ')' {
						name = append(name, '\\')
					}
					name = append(name, unit)
				}
				name = append(name, ')')
			}
		}
		values = append(values, protocol.QuoteJavaScriptUTF16(name))
	}
	return values, nil
}
func rawStringIdentity(value json.RawMessage) string {
	units, e := protocol.DecodeJavaScriptJSONString(value)
	if e != nil {
		return ""
	}
	return string(protocol.QuoteJavaScriptUTF16(units))
}

// ApplyPermissionUpdates runs on the existing permission owner. The controller
// supplies its mode transition so plan exit flags are changed on the same
// Bootstrap authority. Persistent destinations require their real disk writer.
func (s *State) ApplyPermissionUpdates(scope *cooperative.Scope, updates []protocol.JSONObject, setMode func(string) error, persist func(protocol.JSONObject) error) error {
	if s == nil || !s.nativeProcess || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	for _, u := range updates {
		if supportsPermissionPersistence(u.String("destination")) && persist == nil {
			return cap("permission update settings persistence")
		}
	}
	for _, u := range updates {
		destination := u.String("destination")
		switch u.String("type") {
		case "setMode":
			if u.String("mode") == "bypassPermissions" && string(s.context.Get("isBypassPermissionsModeAvailable")) != "true" {
				continue
			}
			if setMode == nil {
				return errors.New("permission updates require actual controller transition")
			}
			if e := setMode(u.String("mode")); e != nil {
				return e
			}
		case "addRules", "replaceRules", "removeRules":
			rules, e := permissionUpdateRules(u)
			if e != nil {
				return e
			}
			key := "alwaysAskRules"
			if u.String("behavior") == "allow" {
				key = "alwaysAllowRules"
			} else if u.String("behavior") == "deny" {
				key = "alwaysDenyRules"
			}
			var collection protocol.JSONObject
			if e := collection.UnmarshalJSON(s.context.Get(key)); e != nil || !collection.IsObject() {
				return errors.New("permission rule provenance is unavailable")
			}
			var current []json.RawMessage
			if value := collection.Get(destination); value != nil {
				if e := json.Unmarshal(value, &current); e != nil {
					return e
				}
			}
			switch u.String("type") {
			case "addRules":
				current = append(current, rules...)
			case "replaceRules":
				current = rules
			case "removeRules":
				remove := map[string]bool{}
				for _, rule := range rules {
					remove[rawStringIdentity(rule)] = true
				}
				kept := []json.RawMessage{}
				for _, rule := range current {
					if !remove[rawStringIdentity(rule)] {
						kept = append(kept, rule)
					}
				}
				current = kept
			}
			if current == nil {
				current = []json.RawMessage{}
			}
			data, _ := json.Marshal(current)
			collection.Set(destination, data)
			data, e = collection.MarshalJSON()
			if e != nil {
				return e
			}
			s.context.Set(key, data)
		case "addDirectories", "removeDirectories":
			var collection protocol.JSONObject
			if e := collection.UnmarshalJSON(s.context.Get("additionalWorkingDirectories")); e != nil {
				return e
			}
			var entries [][]json.RawMessage
			if e := json.Unmarshal(collection.Get("map"), &entries); e != nil {
				return e
			}
			var dirs []json.RawMessage
			if e := json.Unmarshal(u.Get("directories"), &dirs); e != nil {
				return e
			}
			for _, dir := range dirs {
				found := -1
				for i, entry := range entries {
					if len(entry) > 0 && rawStringIdentity(entry[0]) == rawStringIdentity(dir) {
						found = i
						break
					}
				}
				if u.String("type") == "removeDirectories" {
					if found >= 0 {
						entries = append(entries[:found], entries[found+1:]...)
					}
					continue
				}
				payload := protocol.NewJSONObject(protocol.JSONField{Name: "path", Value: dir}, protocol.JSONField{Name: "source", Value: u.Get("destination")})
				data, _ := payload.MarshalJSON()
				entry := []json.RawMessage{dir, data}
				if found >= 0 {
					entries[found] = entry
				} else {
					entries = append(entries, entry)
				}
			}
			if entries == nil {
				entries = [][]json.RawMessage{}
			}
			data, _ := json.Marshal(entries)
			collection.Set("map", data)
			data, e := collection.MarshalJSON()
			if e != nil {
				return e
			}
			s.context.Set("additionalWorkingDirectories", data)
		}
	}
	for _, u := range updates {
		if supportsPermissionPersistence(u.String("destination")) {
			if e := persist(u); e != nil {
				return e
			}
		}
	}
	return nil
}
func supportsPermissionPersistence(destination string) bool {
	return destination == "userSettings" || destination == "projectSettings" || destination == "localSettings"
}
