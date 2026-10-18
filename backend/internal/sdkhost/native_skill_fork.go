package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This is a copy of the live, already filtered parent catalog, not the native
// full tool pool. No handler or mutable catalog object crosses this boundary.
func (h *Host) NativeSkillForkToolNames(scope *cooperative.Scope) ([]string, error) {
	if h == nil || h.options.Print == nil || !h.options.Print.NativeProcess || !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	var names []string
	err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		if h.closing {
			return cooperative.ErrClosed
		}
		names = make([]string, len(h.registered))
		for i, tool := range h.registered {
			names[i] = tool.Name
		}
		return nil
	})
	return names, err
}

// Preserve the existing UTF-16 Set semantics and deny/unknown provenance. This
// value operation grants no owner/settings authority and mutates neither input.
func NativeSkillCommandPermission(permission protocol.JSONObject, rules []json.RawMessage) (protocol.JSONObject, error) {
	if len(rules) == 0 {
		return permission.Clone(), nil
	}
	return mergeNativeSkillCommandRules(permission, rules)
}

// A zero or unminted transfer remains invalid. Keep the original settings,
// merged identity proof and only replace a cloned permission value. The live
// parent produces a fresh transfer on every ASK; this does not cache old policy.
func (value NativeSettingsTransfer) WithCommandRules(rules []json.RawMessage) (NativeSettingsTransfer, error) {
	if value.proof == nil {
		return NativeSettingsTransfer{}, cooperative.ErrRestricted
	}
	permission, err := NativeSkillCommandPermission(value.permission, rules)
	if err != nil {
		return NativeSettingsTransfer{}, err
	}
	value.permission = permission
	return value, nil
}
