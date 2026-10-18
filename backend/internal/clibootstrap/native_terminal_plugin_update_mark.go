package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Manage's update row only toggles its draft marker. The original local-source
// check reads the marketplace entry; it does not invoke the updater operation.
func (m *MainPrint) CheckTerminalPluginUpdate(ctx context.Context, id string) error {
	_, err := m.terminalPluginWorker(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		parts := strings.Split(id, "@")
		if len(parts) < 2 {
			return protocol.NewJSONObject(), nil
		}
		manager, err := m.nativeSettings.pluginCLIMarketplaces(jobs)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		known, err := manager.Known(worker)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		location := mainObj(known.Get(parts[1])).String("installLocation")
		if location == "" {
			return protocol.NewJSONObject(), nil
		}
		market, err := pluginsapi.ReadMarketplace(location)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		var entries []protocol.JSONObject
		_ = json.Unmarshal(market.Get("plugins"), &entries)
		for _, entry := range entries {
			if entry.String("name") == parts[0] {
				if _, err := protocol.DecodeJavaScriptJSONString(entry.Get("source")); err == nil {
					return protocol.JSONObject{}, fmt.Errorf("Local plugins cannot be updated remotely. To update, modify the source at: %s", entry.String("source"))
				}
				break
			}
		}
		return protocol.NewJSONObject(), nil
	})
	return err
}
