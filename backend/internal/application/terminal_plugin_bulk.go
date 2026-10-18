package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalPluginBulkMessage(results []protocol.JSONObject, available []protocol.JSONObject) string {
	completed := 0
	failures := []string{}
	names := map[string]string{}
	for _, row := range available {
		names[row.String("pluginId")] = row.String("name")
	}
	for _, row := range results {
		if string(row.Get("success")) == "true" {
			completed++
		} else {
			failures = append(failures, names[row.String("id")])
		}
	}
	if len(failures) == 0 {
		noun := "plugins"
		if completed == 1 {
			noun = "plugin"
		}
		return fmt.Sprintf("✓ Installed %d %s. Run /reload-plugins to activate.", completed, noun)
	}
	details := []string{}
	for _, row := range results {
		if string(row.Get("success")) == "true" {
			continue
		}
		name := names[row.String("id")]
		if completed == 0 {
			reason := row.String("error")
			if reason == "" {
				reason = "unknown error"
			}
			name += " (" + reason + ")"
		}
		details = append(details, name)
		if len(details) == 2 {
			break
		}
	}
	delimiter := ", "
	if completed == 0 {
		delimiter = "; "
	}
	display := strings.Join(details, delimiter)
	if len(failures) > 2 {
		display += fmt.Sprintf(" and %d more", len(failures)-2)
	}
	if completed == 0 {
		return "Failed to install: " + display
	}
	return fmt.Sprintf("✓ Installed %d of %d plugins. Failed: %s. Run /reload-plugins to activate successfully installed plugins.", completed, len(results), display)
}

func (s *Session) installTerminalPluginSelection(ctx context.Context, state *terminalLocalPanelState) error {
	panel := state.pluginPanel
	if len(panel.selectedForInstall) == 0 {
		return nil
	}
	ids := []string{}
	for _, row := range panel.available {
		id := row.String("pluginId")
		if _, present := panel.selectedForInstall[id]; present {
			ids = append(ids, id)
		}
	}
	result, err := s.process.InstallTerminalPluginSelection(ctx, ids)
	if err != nil {
		return err
	}
	panel.selectedForInstall = nil
	panel.level = ""
	state.props.Notice = terminalPluginBulkMessage(array(result.Get("results")), panel.available)
	return nil
}
