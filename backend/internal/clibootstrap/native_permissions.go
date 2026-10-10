package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func (m *MainPrint) nativePermissionRules() (map[string]sdkpermissions.RuleSet, error) {
	out := map[string]sdkpermissions.RuleSet{}
	for _, source := range m.Startup.EnabledSettingSources() {
		settings, err := m.Startup.GetSettingsForSource(source)
		if err != nil {
			return nil, err
		}
		if settings == nil {
			continue
		}
		var permissions protocol.JSONObject
		if settings.Get("permissions") == nil {
			continue
		}
		if err = permissions.UnmarshalJSON(settings.Get("permissions")); err != nil {
			return nil, err
		}
		rules := sdkpermissions.RuleSet{}
		for key, target := range map[string]*[]string{"allow": &rules.Allow, "deny": &rules.Deny, "ask": &rules.Ask} {
			if raw := permissions.Get(key); raw != nil {
				if err = json.Unmarshal(raw, target); err != nil {
					return nil, err
				}
				var entries []json.RawMessage
				if err = json.Unmarshal(raw, &entries); err != nil {
					return nil, err
				}
				originals := make([]json.RawMessage, len(entries))
				retained := false
				for i, entry := range entries {
					units, e := protocol.DecodeJavaScriptJSONString(entry)
					if e != nil {
						return nil, e
					}
					lone := false
					for j, u := range units {
						if u >= 0xd800 && u <= 0xdbff && (j+1 == len(units) || units[j+1] < 0xdc00 || units[j+1] > 0xdfff) || u >= 0xdc00 && u <= 0xdfff && (j == 0 || units[j-1] < 0xd800 || units[j-1] > 0xdbff) {
							lone = true
							break
						}
					}
					if !lone {
						continue
					}
					parsed, e := sdkpermissions.ParsePermissionRuleValue(entry)
					if e != nil {
						return nil, e
					}
					if parsed.String("toolName") != "Skill" || parsed.Get("ruleContent") == nil {
						continue
					}
					originals[i] = append(json.RawMessage(nil), entry...)
					retained = true
				}
				if retained {
					switch key {
					case "allow":
						rules.RawSkillAllow = originals
					case "deny":
						rules.RawSkillDeny = originals
					case "ask":
						rules.RawSkillAsk = originals
					}
				}

			}
		}
		out[source] = rules
	}
	return out, nil
}
