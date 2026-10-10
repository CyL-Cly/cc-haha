package mcptools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PermissionView borrows the actual connected peers on the same Owner. Close,
// discovery and connection ownership remain solely with the parent service.
func (s *Service) PermissionView(scope *cooperative.Scope, get func(*cooperative.Scope) (protocol.JSONObject, error)) (*Service, error) {
	if s == nil || !scope.BelongsTo(s.options.Owner) || get == nil {
		return nil, cooperative.ErrRestricted
	}
	borrowed := *s
	borrowed.options = s.options
	borrowed.options.PermissionContext = get
	return &borrowed, nil
}
