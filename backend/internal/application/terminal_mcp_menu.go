package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type terminalMCPMenuState struct {
	server protocol.JSONObject
	view   string
	tool   protocol.JSONObject
	auth   *terminalMCPAuthPanelState
}

func terminalMCPCapitalize(name string) string {
	first, size := utf8.DecodeRuneInString(name)
	if size == 0 || first > 0xffff {
		return name
	}
	// Source uppercases charAt(0), which is one UTF-16 code unit. An
	// astral first character therefore remains untouched.
	return cases.Upper(language.Und).String(name[:size]) + name[size:]
}

func terminalMCPMenuOptions(server protocol.JSONObject) []protocol.JSONObject {
	rows := []protocol.JSONObject{}
	add := func(value, label string) {
		rows = append(rows, obj(raw(map[string]string{"value": value, "label": label})))
	}
	status, kind := server.String("status"), obj(server.Get("config")).String("type")
	tools := len(array(server.Get("toolMetadata")))
	remote := kind == "http" || kind == "sse" || kind == "claudeai-proxy"
	if !remote {
		if status != "disabled" && tools > 0 {
			add("tools", "View tools")
		}
		if status != "disabled" {
			add("reconnectMcpServer", "Reconnect")
		}
		if status == "disabled" {
			add("toggle-enabled", "Enable")
		} else {
			add("toggle-enabled", "Disable")
		}
		return rows
	}
	if status == "disabled" {
		add("toggle-enabled", "Enable")
	}
	if status == "connected" && tools > 0 {
		add("tools", "View tools")
	}
	if kind == "claudeai-proxy" {
		if status == "connected" {
			add("claudeai-clear-auth", "Clear authentication")
		} else if status != "disabled" {
			add("claudeai-auth", "Authenticate")
		}
	} else if string(server.Get("isAuthenticated")) == "true" || status == "connected" && tools > 0 {
		add("reauth", "Re-authenticate")
		add("clear-auth", "Clear authentication")
	} else {
		add("auth", "Authenticate")
	}
	if status != "disabled" {
		if status != "needs-auth" {
			add("reconnectMcpServer", "Reconnect")
		}
		add("toggle-enabled", "Disable")
	}
	return rows
}

func terminalMCPToolDisplay(tool protocol.JSONObject) (string, string) {
	annotations := obj(tool.Get("annotations"))
	name := annotations.String("title")
	if name == "" {
		name = tool.String("name")
	}
	labels := []string{}
	for _, entry := range []struct{ key, label string }{{"readOnlyHint", "read-only"}, {"destructiveHint", "destructive"}, {"openWorldHint", "open-world"}} {
		if string(annotations.Get(entry.key)) == "true" {
			labels = append(labels, entry.label)
		}
	}
	return name, strings.Join(labels, ", ")
}

func terminalMCPToolDescription(server, tool protocol.JSONObject) string {
	_, annotations := terminalMCPToolDisplay(tool)
	lines := []string{}
	if annotations != "" {
		lines = append(lines, "["+strings.ReplaceAll(annotations, ", ", "] [")+"]")
	}
	lines = append(lines, "Tool name: "+tool.String("name"), "Full name: "+mcpclient.ToolName(server.String("name"), tool.String("name")))
	if description := tool.String("description"); description != "" {
		lines = append(lines, "Description:\n"+description)
	}
	schema := obj(tool.Get("inputSchema"))
	required := map[string]bool{}
	var names []string
	_ = json.Unmarshal(schema.Get("required"), &names)
	for _, name := range names {
		required[name] = true
	}
	if properties := obj(schema.Get("properties")); len(properties.OwnProperties()) > 0 {
		lines = append(lines, "Parameters:")
		for _, property := range properties.OwnProperties() {
			var name string
			_ = json.Unmarshal(property.NameJSON, &name)
			value := obj(property.Value)
			line := "• " + name
			if required[name] {
				line += " (required)"
			}
			kind := "unknown"
			if value.Get("type") != nil {
				encoded, _ := protocol.JavaScriptStringJSON(value.Get("type"))
				_ = json.Unmarshal(encoded, &kind)
			}
			line += ": " + kind
			if value.Get("description") != nil {
				encoded, _ := protocol.JavaScriptStringJSON(value.Get("description"))
				var description string
				_ = json.Unmarshal(encoded, &description)
				line += " - " + description
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func (s *Session) refreshTerminalMCPPanel(ctx context.Context, state *terminalLocalPanelState) error {
	p := &state.props
	p.MCPCatalog = true
	p.MCPAuthentication = false
	p.MCPExitScope = "list"
	p.PreserveFields = false
	p.SubmitAction = ""
	p.SelectedRow = ""
	p.Rows = nil
	p.Fields = nil
	p.Actions = nil
	p.RawInputKeys = nil
	p.DismissAction = ""
	p.HideDetails = true
	state.rows = map[string]protocol.JSONObject{}
	value, err := s.process.TerminalMCPMenuSnapshot(ctx)
	if err != nil {
		return err
	}
	add := func(id, label, detail, action string, row protocol.JSONObject) {
		state.rows[id] = row.Clone()
		p.Rows = append(p.Rows, tui.LocalPanelRow{ID: id, Label: label, Detail: detail, Actions: []tui.LocalPanelAction{{Key: "enter", Name: action, Label: "select"}}})
	}
	menu := state.mcpMenu
	if menu != nil {
		p.MCPExitScope = "server:" + string(menu.server.Get("name")) + ":" + menu.view
	}
	if menu != nil && menu.auth != nil {
		refreshTerminalMCPAuthentication(state)
		return nil
	}
	if menu == nil {
		p.Title = "MCP servers"
		p.Description = "Manage connections in the current session"
		if p.Kind == "mcp" {
			if state.mcpDiagnostics == nil {
				state.mcpDiagnostics, err = s.process.TerminalMCPParsingDiagnostics(ctx)
				if err != nil {
					return err
				}
			}
			if description := terminalMCPParsingDescription(state.mcpDiagnostics); description != "" {
				p.Description += "\n" + description
			}
		}
		items, err := terminalMCPListItems(array(value.Get("servers")), array(value.Get("agentServers")))
		if err != nil {
			return err
		}
		for _, server := range items {
			if server.String("name") == "ide" {
				continue
			}
			kind := obj(server.Get("config")).String("type")
			if (kind == "http" || kind == "sse") && string(server.Get("agentOnly")) != "true" {
				authenticated, err := s.process.TerminalMCPHasAuthentication(ctx, server.String("name"))
				if err != nil {
					return err
				}
				server.Set("isAuthenticated", raw(authenticated))
			}
			id := server.String("name")
			if string(server.Get("agentOnly")) == "true" {
				id = "agent:" + id
			}
			add(id, server.String("name")+" · "+server.String("status"), "", "mcp-open", server)
			if string(server.Get("agentOnly")) == "true" {
				continue
			}
			p.Rows[len(p.Rows)-1].Actions = append(p.Rows[len(p.Rows)-1].Actions, tui.LocalPanelAction{Key: "t", Name: "toggle", Label: "enable/disable"}, tui.LocalPanelAction{Key: "c", Name: "reconnect", Label: "reconnect"})
		}
		return nil
	}
	// The selected client is retained by the Source detail component. Tool
	// views consume the current AppState catalogue without reconnecting.
	server := menu.server
	current := server
	for _, candidate := range array(value.Get("servers")) {
		if candidate.String("name") == server.String("name") {
			current = candidate
			break
		}
	}
	p.DismissAction = "mcp-back"
	if menu.view == "tool" {
		name, _ := terminalMCPToolDisplay(menu.tool)
		p.Title = name
		p.Description = terminalMCPToolDescription(server, menu.tool)
		return nil
	}
	if menu.view == "tools" {
		tools := array(current.Get("toolMetadata"))
		if server.String("status") != "connected" {
			tools = nil
		}
		p.Title = "Tools for " + server.String("name")
		p.Description = fmt.Sprintf("%d tools", len(tools))
		if len(tools) == 0 {
			p.Description += "\nNo tools available"
		}
		for i, tool := range tools {
			name, annotations := terminalMCPToolDisplay(tool)
			add(fmt.Sprint(i), name, annotations, "mcp-tool", tool)
		}
		if len(p.Rows) > 0 {
			p.SelectedRow = p.Rows[0].ID
		}
		p.HideDetails = false
		return nil
	}
	config := obj(server.Get("config"))
	name := terminalMCPCapitalize(server.String("name"))
	p.Title = name + " MCP Server"
	if string(server.Get("agentOnly")) == "true" {
		var sources []string
		_ = json.Unmarshal(server.Get("sourceAgents"), &sources)
		lines := []string{"agent-only", "Type: " + server.String("transport")}
		if config.String("url") != "" {
			lines = append(lines, "URL: "+config.String("url"))
		}
		if config.String("command") != "" {
			lines = append(lines, "Command: "+config.String("command"))
		}
		lines = append(lines, "Used by: "+strings.Join(sources, ", "), "Status: not connected (agent-only)", "This server connects only when running the agent.")
		if string(server.Get("needsAuth")) == "true" {
			lines = append(lines, "Auth: may need authentication")
			add("auth", "Authenticate", "", "mcp-agent-auth", server)
		}
		add("back", "Back", "", "mcp-back", server)
		p.Description = strings.Join(lines, "\n")
		p.SelectedRow = p.Rows[0].ID
		return nil
	}
	lines := []string{"Status: " + server.String("status")}
	if config.String("type") == "http" || config.String("type") == "sse" || config.String("type") == "claudeai-proxy" {
		lines = append(lines, "URL: "+config.String("url"))
	} else {
		lines = append(lines, "Command: "+config.String("command"))
		var args []string
		_ = json.Unmarshal(config.Get("args"), &args)
		if len(args) > 0 {
			lines = append(lines, "Args: "+strings.Join(args, " "))
		}
	}
	lines = append(lines, "Config location: "+server.String("configLocation"))
	if server.String("status") == "connected" {
		capabilities := []string{}
		for _, entry := range []struct{ key, label string }{{"toolMetadata", "tools"}, {"resources", "resources"}, {"prompts", "prompts"}} {
			if len(array(current.Get(entry.key))) > 0 {
				capabilities = append(capabilities, entry.label)
			}
		}
		if len(capabilities) == 0 {
			capabilities = []string{"none"}
		}
		lines = append(lines, "Capabilities: "+strings.Join(capabilities, " · "))
		if n := len(array(current.Get("toolMetadata"))); n > 0 {
			lines = append(lines, fmt.Sprintf("Tools: %d tools", n))
		}
	}
	p.Description = strings.Join(lines, "\n")
	options := server.Clone()
	options.Set("toolMetadata", current.Get("toolMetadata"))
	for _, option := range terminalMCPMenuOptions(options) {
		add(option.String("value"), option.String("label"), "", "mcp-"+option.String("value"), server)
	}
	if len(p.Rows) > 0 {
		p.SelectedRow = p.Rows[0].ID
	}
	return nil
}

func (s *Session) applyTerminalMCPMenu(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult) error {
	row, exists := state.rows[result.Row]
	switch result.Action {
	case "mcp-open":
		state.props.Notice = ""
		if !exists {
			return errors.New("MCP server is no longer available")
		}
		// MCPListPanel (and its memoized warning child) unmounts when the
		// actual selected server component replaces it.
		state.mcpDiagnostics = nil
		state.mcpMenu = &terminalMCPMenuState{server: row.Clone()}
	case "mcp-back":
		if state.mcpMenu != nil && state.mcpMenu.view == "tool" {
			state.mcpMenu.view = "tools"
		} else if state.mcpMenu != nil && state.mcpMenu.view == "tools" {
			state.mcpMenu.view = ""
		} else {
			state.mcpMenu = nil
			if state.pluginPanel != nil {
				state.pluginPanel.level = ""
			}
		}
	case "mcp-tools":
		state.mcpMenu.view = "tools"
	case "mcp-tool":
		if !exists {
			return errors.New("MCP tool is no longer available")
		}
		state.mcpMenu.tool = row.Clone()
		state.mcpMenu.view = "tool"
	case "mcp-toggle-enabled":
		server := state.mcpMenu.server
		enabled := server.String("status") == "disabled"
		if err := s.process.TerminalMCPControl(ctx, server.String("name"), &enabled); err != nil {
			return err
		}
		state.mcpMenu = nil
		if state.pluginPanel != nil {
			state.pluginPanel.level = ""
		}
	case "mcp-reconnectMcpServer":
		server := state.mcpMenu.server
		if err := s.process.TerminalMCPControl(ctx, server.String("name"), nil); err != nil {
			return fmt.Errorf("Error reconnecting to %s: %w", server.String("name"), err)
		}
		value, err := s.process.TerminalMCPMenuSnapshot(ctx)
		if err != nil {
			return err
		}
		status := ""
		for _, peer := range array(value.Get("servers")) {
			if peer.String("name") == server.String("name") {
				status = peer.String("status")
			}
		}
		switch status {
		case "connected":
			state.props.Notice = "Reconnected to " + server.String("name") + "."
		case "needs-auth":
			state.props.Notice = server.String("name") + " requires authentication. Use the 'Authenticate' option."
		case "failed":
			state.props.Notice = "Failed to reconnect to " + server.String("name") + "."
		default:
			state.props.Notice = "Unknown result when reconnecting to " + server.String("name") + "."
		}
		state.mcpMenu = nil
		if state.pluginPanel != nil {
			state.pluginPanel.level = ""
		}
	default:
		return errors.New("Unknown MCP menu action")
	}
	return nil
}
