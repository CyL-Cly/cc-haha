package clibootstrap

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

const paneTeammatePromptAddendum = "\n# Agent Teammate Communication\n\nIMPORTANT: You are running as an agent in a team. To communicate with anyone on your team:\n- Use the SendMessage tool with `to: \"<name>\"` to send messages to specific teammates\n- Use the SendMessage tool with `to: \"*\"` sparingly for team-wide broadcasts\n\nJust writing a response in text is not visible to others on your team - you MUST use the SendMessage tool.\n\nThe user interacts primarily with the team lead. Your work is coordinated through the task system and teammate messaging.\n"

// CLI panes use the ordinary MAIN prompt/catalog. Desktop approved workers keep
// their frozen preset producer; the shared role binds only identity/lifecycle.
func (m *MainPrint) configureNativePaneTeammate(initial *protocol.JSONObject) error {
	id := *m.options.PaneTeammate
	if !m.options.Native.NativeProcess || m.options.TeamWorker != nil || m.options.DetachedAgent != nil || m.options.AgentID != "" || m.options.Arguments.Resume != "" {
		return mainCap("CLI pane teammate startup identity")
	}
	if id.AgentID != id.AgentName+"@"+id.TeamName || id.AgentName == "" || id.TeamName == "" {
		return fmt.Errorf("invalid teammate agent identity")
	}
	role := sessioncontroller.InProcessTeammateIdentity{AgentID: id.AgentID, AgentName: id.AgentName, TeamName: id.TeamName, Color: id.Color, ParentSessionID: id.ParentSessionID, PlanModeRequired: id.PlanModeRequired}
	binding, err := sessioncontroller.NewPaneTeamContext(m.Owner, role)
	if err != nil {
		return err
	}
	m.teamWorker = &nativeTeamWorkerState{pane: &id, context: binding, preset: &teamworker.Preset{Start: teamworker.Start{Name: id.AgentName, TeamName: id.TeamName, ParentSessionID: id.ParentSessionID}, AgentID: id.AgentID, AgentType: id.AgentType, Source: "built-in", Definition: agentcatalog.Definition{}}}
	m.options.AgentID = id.AgentID
	if m.options.Arguments.AppendSystemPrompt != "" {
		m.options.Arguments.AppendSystemPrompt += "\n\n"
	}
	m.options.Arguments.AppendSystemPrompt += paneTeammatePromptAddendum
	if id.PlanModeRequired {
		m.options.Arguments.PermissionMode = "plan"
		m.options.Arguments.DangerouslySkipPermissions = false
	}
	_ = initial
	return nil
}
