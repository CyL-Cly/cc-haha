package mcptools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type agentAuthenticationContext struct {
	parent      *Service
	process     *Service
	allowsWrite func() bool
}

// BindNativeAgentAuthentication binds the actual captured parent catalogue to
// an already installed Agent snapshot. The native factory additionally proves
// its child Controller/Writer ledger before invoking this method. This is a
// call-context write target, not permission to discover a different catalogue.
func (s *Service) BindNativeAgentAuthentication(scope *cooperative.Scope, binding *ownerasync.NativeBinding, allowsWrite func() bool) error {
	if s == nil || scope == nil || !scope.BelongsTo(s.options.Owner) || !s.options.NativeBinding.Same(binding) || !s.agentResourceSnapshot || s.agentResources == nil || s.agentResources.producer == nil || s.agentResources.producer == s || allowsWrite == nil || s.agentAuthentication != nil {
		return cooperative.ErrRestricted
	}
	parent := s.agentResources.producer
	for _, peer := range s.connections {
		if !s.options.Runtime.OwnsConnection(peer) || !parent.options.Runtime.OwnsConnection(peer) {
			return cooperative.ErrRestricted
		}
	}
	process := parent
	if parent.agentAuthentication != nil {
		process = parent.agentAuthentication.process
	}
	s.agentAuthentication = &agentAuthenticationContext{parent: parent, process: process, allowsWrite: allowsWrite}
	return nil
}

func (s *Service) authenticationProcess() *Service {
	if s.agentAuthentication != nil {
		return s.agentAuthentication.process
	}
	return s
}

func (s *Service) authenticationAppStateTarget() (*Service, bool) {
	if s.agentAuthentication == nil {
		return s, true
	}
	if !s.agentAuthentication.allowsWrite() {
		return s.agentAuthentication.process, false
	}
	return s.agentAuthentication.parent.authenticationAppStateTarget()
}
