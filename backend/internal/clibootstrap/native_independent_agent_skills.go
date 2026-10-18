package clibootstrap

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
)

type nativeAgentPresetLife struct {
	mu      sync.Mutex
	closed  bool
	cleanup func()
}

func permissionFeedbackValueNative(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null" && string(raw) != "false" && string(raw) != `""` && string(raw) != "[]" && string(raw) != "{}"
}

// Ordinary foreground/background Agent runners consume the same parent command
// producer as Workflow. The original skill envelopes precede the admitted child
// prompt and are persisted by that child's existing Controller and Writer.
func (m *MainPrint) installNativeIndependentAgentSkills(ctx context.Context, scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	d := m.options.DetachedAgent
	if d == nil || d.preset == nil {
		return nil
	}
	if err := d.preset.registerHooks(m, scope); err != nil {
		return err
	}
	rows, err := d.preset.skills(ctx, m, scope)
	if err != nil {
		return err
	}
	if err = m.Host.InstallNativeAgentPrelude(scope, rows); err != nil {
		return err
	}
	additional, inline, err := d.preset.connectMCP(ctx, m, scope)
	cleanup := func() {
		for _, peer := range inline {
			_ = m.mcpRuntime.CloseConnection(context.Background(), peer)
		}
	}
	if err != nil {
		cleanup()
		return err
	}
	if err = m.mcpTools.InstallAgentConnections(scope, d.preset.inherited, additional, d.preset.resources); err != nil {
		cleanup()
		return err
	}
	names := []string{}
	for _, peer := range additional {
		if peer.Status == "connected" {
			for _, tool := range peer.Tools {
				names = append(names, mcpclient.ToolName(peer.Name, tool.String("name")))
			}
		}
	}
	if err = m.Host.AppendNativeAgentMCPTools(scope, names); err != nil {
		cleanup()
		return err
	}
	life := &m.nativeAgentPresetLife
	life.mu.Lock()
	if life.closed {
		life.mu.Unlock()
		cleanup()
		return cooperative.ErrClosed
	}
	life.cleanup = cleanup
	life.mu.Unlock()
	return nil
}

func (m *MainPrint) closeNativeAgentPreset() {
	if m == nil {
		return
	}
	if m.hook != nil && m.options.DetachedAgent != nil && m.options.DetachedAgent.preset != nil {
		m.hook.ClearNativeAgentHooks(m.options.AgentID)
	}
	life := &m.nativeAgentPresetLife
	life.mu.Lock()
	life.closed = true
	cleanup := life.cleanup
	life.cleanup = nil
	life.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}
