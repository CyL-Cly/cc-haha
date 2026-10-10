package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpcli"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/charmbracelet/x/term"
)

type mcpImportModel struct {
	selection          mcpcli.DesktopSelection
	cursor             int
	accepted, canceled bool
}

func (m *mcpImportModel) Init() tea.Cmd { return nil }
func (m *mcpImportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor+1 < len(m.selection.Names) {
			m.cursor++
		}
	case "space", " ":
		if len(m.selection.Names) == 0 {
			break
		}
		name := m.selection.Names[m.cursor]
		for i, selected := range m.selection.Selected {
			if selected == name {
				m.selection.Selected = append(m.selection.Selected[:i], m.selection.Selected[i+1:]...)
				return m, nil
			}
		}
		m.selection.Selected = append(m.selection.Selected, name)
	case "enter":
		m.accepted = true
		return m, tea.Quit
	case "esc", "ctrl+c":
		m.canceled = true
		return m, tea.Quit
	}
	return m, nil
}
func (m *mcpImportModel) View() tea.View {
	var text strings.Builder
	fmt.Fprintf(&text, "Import MCP Servers from Claude Desktop\nFound %d MCP server", len(m.selection.Names))
	if len(m.selection.Names) != 1 {
		text.WriteByte('s')
	}
	text.WriteString(" in Claude Desktop.\n")
	for _, name := range m.selection.Names {
		if m.selection.Existing.Get(name) != nil {
			text.WriteString("Note: Some servers already exist with the same name. If selected, they will be imported with a numbered suffix.\n")
			break
		}
	}
	text.WriteString("Please select the servers you want to import:\n")
	for i, name := range m.selection.Names {
		cursor, selected := " ", " "
		if i == m.cursor {
			cursor = ">"
		}
		for _, entry := range m.selection.Selected {
			if name == entry {
				selected = "x"
				break
			}
		}
		fmt.Fprintf(&text, "%s [%s] %s", cursor, selected, name)
		if m.selection.Existing.Get(name) != nil {
			text.WriteString(" (already exists)")
		}
		text.WriteByte('\n')
	}
	text.WriteString("Space select · Enter confirm · Esc cancel\n")
	return tea.NewView(text.String())
}

func importMCPDesktop(ctx context.Context, platform, home string, environment map[string]string, input *os.File, output io.Writer, scope string, all func(context.Context) (mcpconfig.Result, error), editor *mcpconfig.Editor) (err error) {
	if platform == "darwin" {
		platform = "macos"
	} else if platform == "windows" {
		platform = "windows"
	} else if platform == "linux" {
		if raw, err := os.ReadFile("/proc/version"); err == nil && (strings.Contains(strings.ToLower(string(raw)), "microsoft") || strings.Contains(strings.ToLower(string(raw)), "wsl")) {
			platform = "wsl"
		}
	}
	servers, err := mcpcli.ReadDesktopServers(mcpcli.DesktopFiles{Platform: platform, Home: home, UserProfile: environment["USERPROFILE"], Stat: os.Stat, ReadDir: os.ReadDir, ReadFile: os.ReadFile})
	if err != nil {
		return err
	}
	if len(servers.OwnProperties()) == 0 {
		_, err = fmt.Fprintln(output, "No MCP servers found in Claude Desktop configuration or configuration file does not exist.")
		return err
	}
	if input == nil || !term.IsTerminal(input.Fd()) {
		return errors.New("Claude Desktop MCP import requires an interactive terminal for server selection")
	}
	existing, err := all(ctx)
	if err != nil {
		return err
	}
	model := &mcpImportModel{selection: mcpcli.NewDesktopSelection(servers, existing.Servers)}
	terminalState, err := term.GetState(input.Fd())
	if err != nil {
		return err
	}
	// Bubble Tea restores raw mode internally but discards restoration errors.
	// This second restoration preserves the caller's exact original settings.
	defer func() { err = errors.Join(err, term.Restore(input.Fd(), terminalState)) }()
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output), tea.WithEnvironment(mcpEnvironment(environment)))
	if _, err = program.Run(); err != nil {
		return err
	}
	if model.canceled {
		_, err = fmt.Fprint(output, "\nNo servers were imported.")
		return err
	}
	if !model.accepted {
		return errors.New("Claude Desktop MCP selection did not complete")
	}
	count, err := mcpcli.ImportDesktopSelection(ctx, servers, model.selection, scope, func(ctx context.Context, name, scope string, raw []byte) error {
		return editor.Add(ctx, name, scope, raw)
	})
	if err != nil {
		return err
	}
	if count == 0 {
		_, err = fmt.Fprint(output, "\nNo servers were imported.")
	} else {
		plural := "server"
		if count != 1 {
			plural = "servers"
		}
		_, err = fmt.Fprintf(output, "\nSuccessfully imported %d MCP %s to %s config.\n", count, plural, scope)
	}
	return err
}
func mcpEnvironment(env map[string]string) []string {
	out := []string{}
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}
