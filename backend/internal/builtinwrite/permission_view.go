package builtinwrite

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) PermissionView(scope *cooperative.Scope, get func(*cooperative.Scope) (protocol.JSONObject, error)) (*Service, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if get == nil {
		return nil, cooperative.ErrRestricted
	}
	borrowed := *s
	borrowed.options = s.options
	borrowed.options.PermissionContext = get
	return &borrowed, nil
}
