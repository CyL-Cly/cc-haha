package tui

import (
	"math"
	"strings"
)

func (p *localPanelState) terminalHelpFlexRoot(width, height int, theme string, bindings []Keybinding, modal ...bool) *terminalHelpFlex {
	insideModal := len(modal) > 0 && modal[0]
	data := p.terminalHelpData()
	blue := terminalHelpBlue(theme)
	header := helpRow(helpText("\x1b[1m" + blue + "Claude Code v" + displayText(data.Version) + "\x1b[39;22m"))
	header.gap = 1
	for _, tab := range []string{"general", "commands", "custom-commands"} {
		label := " " + tab + " "
		if tab == p.terminalHelpTab() {
			if p.headerFocused {
				background := strings.Replace(blue, "[38;", "[48;", 1)
				background = strings.Replace(background, "[94m", "[104m", 1)
				label = "\x1b[1m" + TerminalThemeRoleEscape(theme, "inverseText", false) + background + label + "\x1b[39;49;22m"
			} else {
				label = "\x1b[1;7m" + label + "\x1b[27;22m"
			}
		}
		header.children = append(header.children, helpText(label))
	}
	var body *terminalHelpFlex
	if p.terminalHelpTab() == "general" {
		state := NewState(nil, max(1, width-4), height)
		state.ShortcutHelp = data.Shortcuts
		state.bindings = bindings
		columns := state.shortcutHelpColumns()
		menu := helpRow()
		menu.gap = 2
		for i, labels := range columns {
			column := helpColumn()
			if i == 0 {
				column.fixedWidth = 24
			} else if i == 1 {
				column.fixedWidth = 35
			}
			for _, label := range labels {
				column.children = append(column.children, helpRow(helpText(displayText(label))))
			}
			menu.children = append(menu.children, column)
		}
		shortcuts := helpColumn(helpRow(helpText("\x1b[1mShortcuts\x1b[22m")), menu)
		body = helpColumn(helpRow(helpText("Claude understands your codebase, makes edits with your permission, and executes commands — right from your terminal.")), shortcuts)
		body.py = 1
		body.gap = 1
	} else {
		commands := p.terminalHelpCommands()
		body = helpColumn()
		body.py = 1
		if len(commands) == 0 && p.terminalHelpTab() == "custom-commands" {
			body.children = append(body.children, helpRow(helpText(terminalPillStyle("No custom commands found", theme, "inactive", false, false))))
		} else {
			title := "Browse default commands:"
			if p.terminalHelpTab() == "custom-commands" {
				title = "Browse custom commands:"
			}
			body.children = append(body.children, helpText(title))
			list := helpColumn()
			visible := max(1, (height/2-10)/2)
			start := p.scroll
			end := min(len(commands), start+visible)
			for i := start; i < end; i++ {
				focused := !p.headerFocused && i == p.selected
				role, prefix := "", "  "
				if focused {
					role = "suggestion"
					prefix = terminalPillStyle("❯", theme, role, false, false) + " "
				} else if i == start && start > 0 {
					prefix = terminalPillStyle("↑", theme, "inactive", false, false) + " "
				} else if i == end-1 && end < len(commands) {
					prefix = terminalPillStyle("↓", theme, "inactive", false, false) + " "
				}
				entry := helpColumn(helpRow(helpText(prefix), helpText(terminalPillStyle("/"+displayText(commands[i].String("name")), theme, role, false, false))))
				entry.key = commands[i].String("name")
				entry.noShrink = true
				description := terminalHelpDescription(commands[i].String("description"), width-10)
				if description != "" {
					role = "inactive"
					descriptionNode := helpRow(helpText(terminalPillStyle(description, theme, role, false, false)))
					descriptionNode.pl = 4
					entry.children = append(entry.children, descriptionNode)
				}
				list.children = append(list.children, entry)
			}
			listBox := helpRow(list)
			listBox.margin = 1
			body.children = append(body.children, listBox)
		}
	}
	body.key = p.terminalHelpTab()
	tabBody := helpRow(helpRow(body))
	tabBody.margin = 1
	tabs := helpColumn(header, tabBody)
	if insideModal {
		header.noShrink, tabBody.noShrink, tabs.noShrink = true, true, true
		// Tabs supplies an actual column ScrollBox in ModalContext. It has no
		// key-driven scroll: the root transcript owns Scroll keybindings.
		tabBody.children = []*terminalHelpFlex{helpColumn(helpRow(body))}
		tabBody.children[0].noShrink = true
		tabBody.children[0].children[0].noShrink = true
	}
	help := helpRow(helpText("For more help: https://code.claude.com/docs/en/overview"))
	help.margin = 1
	dismiss := "Esc"
	if value, ok := BindingDisplay("help:dismiss", "Help", bindings); ok {
		dismiss = value
	}
	exit := helpRow(helpText("\x1b[3m" + terminalPillStyle(dismiss+" to cancel", theme, "inactive", false, false) + "\x1b[23m"))
	if p.helpExitPending != "" {
		exit = helpRow(helpText(terminalPillStyle("Press "+p.helpExitPending+" again to exit", theme, "inactive", false, false)))
	}
	exit.margin = 1
	content := helpColumn(tabs, help, exit)
	if insideModal {
		content.px = 1
		content.noShrink = true
		outer := helpColumn(content)
		outer.px, outer.noShrink = 2, true
		return outer
	}
	content.px = 2
	pane := helpColumn(helpText(blue+strings.Repeat("─", max(1, width))+"\x1b[39m"), content)
	pane.top = 1
	return helpColumn(pane)
}

func (p *localPanelState) terminalFullscreenHelpLines(width, height int, theme string, bindings []Keybinding) []string {
	root := p.terminalHelpFlexRoot(width, height, theme, bindings, true)
	root.layout(float64(max(1, width)), math.NaN(), true, false, false)
	measured := max(1, int(math.Ceil(root.h)))
	// The original opaque modal repaints its body against a fresh clipped
	// screen, instead of using scrollback Help's clean-node overflow cache.
	var paint *terminalHelpPaint
	rows := terminalHelpFlexRows(root, max(1, width), measured, &paint)
	if len(rows) > max(0, height-3) {
		rows = rows[:max(0, height-3)]
	}
	divider := TerminalThemeRoleEscape(theme, "permission", false) + strings.Repeat("▔", max(1, width)) + "\x1b[39m"
	return append([]string{divider}, rows...)
}

func (p *localPanelState) terminalHelpFlexLines(width, height int, theme string, bindings []Keybinding) []string {
	return terminalHelpFlexRows(p.terminalHelpFlexRoot(width, height, theme, bindings), max(1, width), max(1, height/2), &p.helpPaint)
}
