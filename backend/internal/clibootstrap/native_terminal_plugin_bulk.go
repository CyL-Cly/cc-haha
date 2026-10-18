package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The selection arrives in the actual Discover catalog order. The original UI
// commits each installation in turn and clears plugin caches after failures as
// well as successes, without entering the single-install options dialog.
func (m *MainPrint) InstallTerminalPluginSelection(ctx context.Context, ids []string) (protocol.JSONObject, error) {
	return m.terminalPluginCacheEdit(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		service, err := m.nativeSettings.pluginCLIOperations(m.pluginOptions, jobs)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		results := []protocol.JSONObject{}
		for _, id := range ids {
			row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoteMain(id)})
			_, err := service.InstallInteractive(worker, id, "user")
			row.Set("success", rawMain(err == nil))
			if err != nil {
				row.Set("error", quoteMain(err.Error()))
			}
			results = append(results, row)
		}
		if err := m.nativeSettings.pluginCLIClear(worker); err != nil {
			return protocol.JSONObject{}, err
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "results", Value: rawMain(results)}), nil
	})
}
