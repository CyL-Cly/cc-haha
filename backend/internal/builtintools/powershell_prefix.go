package builtintools

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/powershellpolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PowerShellApprovalPrefixes reuses the exact AST cache already consumed by
// permission evaluation. UI suggestions never change the policy decision or
// its exact persisted suggestions; only an explicit user selection creates
// the separately editable rule.
func (s *Service) PowerShellApprovalPrefixes(scope *cooperative.Scope, command string) ([]string, error) {
	parser, classifier, err := s.powerShellPolicy(scope)
	if err != nil {
		return nil, err
	}
	future, err := native(s, scope, func() (powershellpolicy.Parsed, error) { return parser.Parse(context.Background(), command), nil })
	if err != nil {
		return nil, err
	}
	parsed, err := future.Await(scope)
	if err != nil {
		return nil, err
	}
	catalog, err := powershellpolicy.NewPrefixCatalog()
	if err != nil {
		return nil, err
	}
	return catalog.Compound(parsed, func(command powershellpolicy.Command) bool { return classifier.Allowlisted(command, command.Text) }), nil
}

// The Source confirmation's initial fallback is the literal command; multiline
// literals suppress that editor. A successful static extraction refines it.
func PowerShellApprovalInitialPrefix(command string, prefixes []string) *string {
	if len(prefixes) > 0 {
		value := prefixes[0] + ":*"
		return &value
	}
	for _, r := range command {
		if r == '\n' {
			return nil
		}
	}
	value := command
	return &value
}

func PowerShellEditedPermissionUpdates(prefix string) []protocol.JSONObject {
	prefix = protocol.TrimJavaScriptString(prefix)
	if prefix == "" {
		return []protocol.JSONObject{}
	}
	return []protocol.JSONObject{protocol.NewJSONObject(field("type", "addRules"), field("rules", []protocol.JSONObject{protocol.NewJSONObject(field("toolName", "PowerShell"), field("ruleContent", prefix))}), field("behavior", "allow"), field("destination", "localSettings"))}
}
