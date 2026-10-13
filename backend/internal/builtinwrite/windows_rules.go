package builtinwrite

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func (s *Service) windowsDenyRules(context protocol.JSONObject, path string, wholeOnly bool) (wholeDeny, editDeny bool, err error) {
	var sources protocol.JSONObject
	if err = sources.UnmarshalJSON(context.Get("alwaysDenyRules")); err != nil {
		return
	}
	for _, field := range sources.OwnProperties() {
		var source string
		if err = json.Unmarshal(field.NameJSON, &source); err != nil {
			return
		}
		var rules []json.RawMessage
		if err = json.Unmarshal(field.Value, &rules); err != nil {
			return
		}
		for _, rule := range rules {
			parsed, parseErr := sdkpermissions.ParsePermissionRuleValue(rule)
			if parseErr != nil {
				err = parseErr
				return
			}
			if parsed.String("toolName") == "Write" && parsed.Get("ruleContent") == nil {
				wholeDeny = true
				continue
			}
			if wholeOnly {
				continue
			}
		}
	}
	if !wholeOnly {
		var patterns []nativepaths.PermissionPattern
		patterns, err = nativepaths.WindowsPatterns(context, "alwaysDenyRules", "Edit")
		if err == nil {
			editDeny, err = nativepaths.MatchWindowsPatterns(path, s.options.CWD, s.options.Home, s.options.PermissionRoots, patterns)
		}
	}
	return
}
