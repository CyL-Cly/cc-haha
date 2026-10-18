package mcptools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// NativeAgentPresetProfile checks the concrete MCP graph constructed for this
// Owner. A finite Agent callback cannot substitute a foreign runtime/config or
// the native reference ledger that will own its asynchronous child connection.
func (s *Service) NativeAgentPresetProfile(scope *cooperative.Scope, owner *cooperative.Owner, binding *ownerasync.NativeBinding, runtime *mcpruntime.Service, configurations *mcpconfig.Service) bool {
	return s != nil && scope.BelongsTo(owner) && s.options.Owner == owner && s.options.NativeBinding.Same(binding) && runtime != nil && s.options.Runtime == runtime && configurations != nil && s.options.Configurations == configurations
}
