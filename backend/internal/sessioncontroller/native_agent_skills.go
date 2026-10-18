package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// NativeAgentSkillPreloadProfile proves the existing native parent Controller
// and its actual Writer ledger. Finite hosts and another parent's ledger cannot
// enable the canonical native child prelude by supplying an Agent callback.
func (c *Controller) NativeAgentSkillPreloadProfile(scope *cooperative.Scope, owner *cooperative.Owner, binding *ownerasync.NativeBinding) bool {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(owner) || !c.options.Bootstrap.Owns(owner) || !c.options.PublicPrint || !c.options.NativeTools || !c.options.AsyncPersistence || c.writer == nil || c.writer.async == nil {
		return false
	}
	return c.writer.async.NativeCopyBinding().Same(binding)
}
