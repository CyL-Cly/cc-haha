package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (h *Host) HasRegisteredTool(scope *cooperative.Scope, name string) (bool, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) {
		return false, cooperative.ErrRestricted
	}
	if e := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); e != nil {
		return false, e
	}
	for _, tool := range h.registered {
		if tool.Name == name {
			return true, nil
		}
	}
	return false, nil
}
