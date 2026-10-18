package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func (m *MainPrint) bindNativeAgentAuthentication(scope *cooperative.Scope, binding *ownerasync.NativeBinding) error {
	d := m.options.DetachedAgent
	if d == nil || d.authParent == nil {
		return nil
	}
	if !m.detachedConstructed || m.Controller == nil || !m.Controller.NativeAgentSkillPreloadProfile(scope, m.Owner, binding) || m.mcpTools == nil || !m.mcpTools.NativeAgentPresetProfile(scope, m.Owner, binding, m.mcpRuntime, m.mcpConfigurations) {
		return cooperative.ErrRestricted
	}
	// Only the actual foreground runner installs this atomic background-state
	// reader before constructing its child. Async agents have a no-op writer;
	// foreground agents promoted later lose their parent AppState write channel.
	return m.mcpTools.BindNativeAgentAuthentication(scope, binding, func() bool {
		return d.isBackgrounded != nil && !d.isBackgrounded()
	})
}
