package sdkpermissions

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ParsePermissionRuleValue is the original shared UTF-16 permission-rule
// parser. Hook predicates and rule-only permission checks retain this identity.
func ParsePermissionRuleValue(raw json.RawMessage) (protocol.JSONObject, error) {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	unescaped := func(index int) bool {
		slashes := 0
		for j := index - 1; j >= 0 && units[j] == '\\'; j-- {
			slashes++
		}
		return slashes%2 == 0
	}
	open, close := -1, -1
	for i, u := range units {
		if u == '(' && unescaped(i) {
			open = i
			break
		}
	}
	for i := len(units) - 1; i >= 0; i-- {
		if units[i] == ')' && unescaped(i) {
			close = i
			break
		}
	}
	name := units
	var content []uint16
	if open > 0 && close > open && close == len(units)-1 {
		name = units[:open]
		content = units[open+1 : close]
		if len(content) == 1 && content[0] == '*' {
			content = nil
		}
		if len(content) > 0 {
			unescape := func(in []uint16, pattern []uint16, replacement uint16) []uint16 {
				out := []uint16{}
				for i := 0; i < len(in); i++ {
					match := i+len(pattern) <= len(in)
					if match {
						for j, c := range pattern {
							if in[i+j] != c {
								match = false
								break
							}
						}
					}
					if match {
						out = append(out, replacement)
						i += len(pattern) - 1
					} else {
						out = append(out, in[i])
					}
				}
				return out
			}
			content = unescape(content, []uint16{'\\', '('}, '(')
			content = unescape(content, []uint16{'\\', ')'}, ')')
			content = unescape(content, []uint16{'\\', '\\'}, '\\')
		}
	}
	nameJSON := protocol.QuoteJavaScriptUTF16(name)
	for legacy, canonical := range map[string]string{"Task": "Agent", "KillShell": "TaskStop", "AgentOutputTool": "TaskOutput", "BashOutputTool": "TaskOutput"} {
		var key []uint16
		key, _ = protocol.DecodeJavaScriptJSONString(quote(legacy))
		if rawStringIdentity(nameJSON) == rawStringIdentity(protocol.QuoteJavaScriptUTF16(key)) {
			nameJSON = quote(canonical)
			break
		}
	}
	rule := protocol.NewJSONObject(protocol.JSONField{Name: "toolName", Value: nameJSON})
	if len(content) > 0 {
		rule.Set("ruleContent", protocol.QuoteJavaScriptUTF16(content))
	}
	return rule, nil
}
