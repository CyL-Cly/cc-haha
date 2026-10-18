package clibootstrap

import (
	"context"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The shared-project confirmation writes an override directly. Unlike the
// ordinary disable operation, an already-false local override is successful.
func (m *MainPrint) DisableTerminalPluginLocally(ctx context.Context, id string) (protocol.JSONObject, error) {
	return m.terminalPluginCacheEdit(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		value := false
		if err := m.nativeSettings.pluginCLIWriteEnabled(worker, "local", "", id, &value, jobs); err != nil {
			return protocol.JSONObject{}, fmt.Errorf("Failed to write settings: %w", err)
		}
		if err := m.nativeSettings.pluginCLIClear(worker); err != nil {
			return protocol.JSONObject{}, err
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: quoteMain("✓ Disabled " + strings.Split(id, "@")[0] + " in .claude/settings.local.json. Run /reload-plugins to apply.")}), nil
	})
}
