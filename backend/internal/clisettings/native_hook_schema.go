package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
)

// The native settings path consumes the same shared HooksSchema declarations
// as plugin/frontmatter parsing. The older finite profile remains unchanged.
func parseNativeHooks(raw json.RawMessage, file string) (json.RawMessage, []json.RawMessage, error) {
	if raw == nil {
		return nil, nil, nil
	}
	schemas, e := pluginschema.Declarations()
	if e != nil {
		return nil, nil, e
	}
	parsed, issues := pluginschema.Validate(schemas.Hooks, raw)
	errors := []json.RawMessage{}
	for _, issue := range issues {
		issue.Path = append([]any{"hooks"}, issue.Path...)
		errors = append(errors, formatNativePluginIssue(file, issue))
	}
	return parsed, errors, nil
}
