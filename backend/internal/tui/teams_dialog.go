package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

func (p *localPanelState) retainTerminalTeamsState(old *localPanelState) {
	if old == nil || old.props.ID != p.props.ID || old.props.Kind != "teams" || p.props.Kind != "teams" {
		return
	}
	current, previous := presentationObject(p.props.Presentation), presentationObject(old.props.Presentation)
	if current.String("level") == "list" {
		p.selected = old.selected
		if previous.String("level") == "detail" {
			p.selected = 0
		}
		if string(current.Get("selectionRevision")) != string(previous.Get("selectionRevision")) && len(current.Get("selectionMaximum")) > 0 {
			var maximum int
			_ = json.Unmarshal(current.Get("selectionMaximum"), &maximum)
			p.selected = max(0, min(p.selected, maximum))
		}
	}
	if current.String("level") == "detail" && previous.String("level") == "detail" && current.String("memberName") == previous.String("memberName") {
		p.teamsPromptExpanded = old.teamsPromptExpanded
	}
}

func terminalTeamMode(member protocol.JSONObject, theme string, bold bool) string {
	mode := member.String("mode")
	symbol, role := "", ""
	switch mode {
	case "plan":
		symbol, role = "⏸", "planMode"
	case "acceptEdits":
		symbol, role = "⏵⏵", "autoAccept"
	case "bypassPermissions", "dontAsk":
		symbol, role = "⏵⏵", "error"
	case "auto":
		symbol, role = "⏵⏵", "warning"
	}
	if symbol == "" {
		return ""
	}
	return terminalPillStyle(symbol+" ", theme, role, false, bold)
}

func (s *State) handleTerminalTeamsKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || p.props.Kind != "teams" {
		return false, nil
	}
	value := presentationObject(p.props.Presentation)
	detail := value.String("level") == "detail"
	selectedIndex := func() int {
		index := p.selected
		if detail {
			var members []protocol.JSONObject
			_ = json.Unmarshal(value.Get("members"), &members)
			index = -1
			for i, member := range members {
				if member.String("name") == value.String("memberName") {
					index = i
					break
				}
			}
		}
		return index
	}
	validMember := func() bool { index := selectedIndex(); return index >= 0 && index < len(p.props.Rows) }
	result := func(name string) []action {
		index := selectedIndex()
		row := ""
		if index >= 0 && index < len(p.props.Rows) {
			row = p.props.Rows[index].ID
		}
		result := LocalPanelResult{ID: p.props.ID, Action: name, Row: row, TeamsFrame: p.props.TeamsFrame, TeamsIndex: index, TeamsLevel: value.String("level")}
		return []action{{kind: actionLocalPanel, panel: result}}
	}
	resolution := ResolveChord(keyStroke(key), []string{"Global", "Confirmation"}, s.bindings, s.pendingChord)
	switch resolution.Type {
	case "chord_started":
		s.pendingChord, s.chordUntil = resolution.Pending, now.Add(time.Second)
		return true, nil
	case "unbound":
		s.pendingChord = nil
		return true, nil
	case "chord_cancelled":
		s.pendingChord = nil
	case "match":
		s.pendingChord = nil
		switch resolution.Action {
		case "confirm:cycleMode":
			if len(p.props.Rows) == 0 || detail && !validMember() {
				return true, nil
			}
			return true, result("teams-cycle-mode")
		case "confirm:no":
			if detail {
				return true, result("teams-back")
			}
			s.LocalPanel = nil
			return true, result("close")
		case "app:interrupt", "app:exit":
			if s.press("teams:"+resolution.Action, now) {
				return true, s.EOF()
			}
			return true, nil
		}
	}
	if string(value.Get("loading")) == "true" {
		return true, nil
	}
	name := key.Name
	if name == "" {
		name = key.Text
	}
	switch name {
	case "left":
		if detail {
			return true, result("teams-back")
		}
	case "up":
		p.selected = max(0, p.selected-1)
	case "down":
		maximum := max(0, len(p.props.Rows)-1)
		if detail {
			maximum = 0
		}
		p.selected = min(maximum, p.selected+1)
	case "enter":
		if validMember() {
			if detail {
				return true, result("teams-view")
			}
			return true, result("teams-detail")
		}
	case "k":
		if validMember() {
			return true, result("teams-kill")
		}
	case "s":
		if validMember() {
			return true, result("teams-shutdown")
		}
	case "h":
		if validMember() && string(value.Get("supportsHideShow")) == "true" {
			return true, result("teams-hide-toggle")
		}
	case "H":
		if !detail && string(value.Get("supportsHideShow")) == "true" {
			return true, result("teams-hide-all-toggle")
		}
	case "p":
		if detail {
			p.teamsPromptExpanded = !p.teamsPromptExpanded
		} else {
			return true, result("teams-prune-idle")
		}
	}
	return true, nil
}

func (p *localPanelState) terminalTeamsLines(width, height int, theme string, bindings []Keybinding) []string {
	value := presentationObject(p.props.Presentation)
	var members, tasks []protocol.JSONObject
	_ = json.Unmarshal(value.Get("members"), &members)
	_ = json.Unmarshal(value.Get("tasks"), &tasks)
	level := value.String("level")
	title := "Team " + value.String("teamName")
	subtitle := fmt.Sprintf("%d teammate%s", len(members), map[bool]string{true: "", false: "s"}[len(members) == 1])
	body := []string{}
	titleNode := ""
	if level == "detail" {
		member := protocol.JSONObject{}
		for _, candidate := range members {
			if candidate.String("name") == value.String("memberName") {
				member = candidate
				break
			}
		}
		if !member.IsObject() {
			return nil
		}
		title = "@" + member.String("name")
		role := "background"
		if color := member.String("color"); TerminalThemeRole(theme, color+"_FOR_SUBAGENTS_ONLY") != "" {
			role = color + "_FOR_SUBAGENTS_ONLY"
		}
		titleNode = terminalTeamMode(member, theme, true) + terminalPillStyle(title, theme, role, false, true)
		subparts := []string{}
		if model := member.String("model"); model != "" {
			subparts = append(subparts, model)
		}
		path := member.String("worktreePath")
		if path != "" {
			path = "worktree: " + path
		} else {
			path = member.String("cwd")
		}
		if path != "" {
			subparts = append(subparts, path)
		}
		subtitle = strings.Join(subparts, " · ")
		if len(tasks) > 0 {
			body = append(body, "\x1b[1mTasks\x1b[22m")
			for _, task := range tasks {
				marker, role := "◼", ""
				if task.String("status") == "completed" {
					marker, role = "✔", "success"
				}
				body = append(body, terminalPillStyle(marker+" "+task.String("subject"), theme, role, false, false))
			}
		}
		if prompt := member.String("prompt"); prompt != "" {
			prompt = displayText(prompt)
			if len(tasks) > 0 {
				body = append(body, "")
			}
			body = append(body, "\x1b[1mPrompt\x1b[22m")
			if !p.teamsPromptExpanded && ansi.StringWidth(prompt) > 80 {
				prompt = ansi.Truncate(displayText(prompt), 80, "…") + terminalPillStyle(" (p to expand)", theme, "inactive", false, false)
			}
			body = append(body, prompt)
		}
	} else if len(members) == 0 {
		body = append(body, terminalPillStyle("No teammates", theme, "inactive", false, false))
	} else {
		for i, member := range members {
			prefix, role := "  ", ""
			if i == p.selected {
				prefix, role = "❯ ", "suggestion"
			}
			if member.String("status") == "idle" && i != p.selected {
				role = "inactive"
			}
			label := terminalPillStyle(prefix, theme, role, false, false)
			if string(member.Get("isHidden")) == "true" {
				label += terminalPillStyle("[hidden] ", theme, "inactive", false, false)
			}
			if member.String("status") == "idle" {
				label += terminalPillStyle("[idle] ", theme, "inactive", false, false)
			}
			label += terminalTeamMode(member, theme, false) + terminalPillStyle("@"+member.String("name"), theme, role, false, false)
			if model := member.String("model"); model != "" {
				label += terminalPillStyle(" ("+model+")", theme, "inactive", false, false)
			}
			body = append(body, label)
		}
	}
	out := []string{"", TerminalThemeRoleEscape(theme, "background", false) + strings.Repeat("─", max(1, width)) + "\x1b[39m"}
	if titleNode == "" {
		titleNode = terminalPillStyle(title, theme, "background", false, true)
	}
	for _, line := range terminalMarkdownWrap(titleNode, max(1, width-4)) {
		out = append(out, "  "+line)
	}
	if subtitle != "" {
		for _, line := range terminalMarkdownWrap(terminalPillStyle(subtitle, theme, "inactive", false, false), max(1, width-4)) {
			out = append(out, "  "+line)
		}
	}
	out = append(out, "")
	for _, row := range body {
		for _, line := range terminalMarkdownWrap(row, max(1, width-4)) {
			out = append(out, "  "+line)
		}
	}
	cycle := "shift+tab"
	if custom, ok := BindingDisplay("confirm:cycleMode", "Confirmation", bindings); ok {
		cycle = custom
	}
	guide := "↑/↓ select · Enter view · k kill · s shutdown · p prune idle"
	if level == "detail" {
		guide = "← back · Esc close · k kill · s shutdown"
	}
	if string(value.Get("supportsHideShow")) == "true" {
		guide += " · h hide/show"
		if level != "detail" {
			guide += " · H hide/show all"
		}
	}
	if level == "detail" {
		guide += " · " + cycle + " cycle mode"
	} else {
		guide += " · " + cycle + " sync cycle modes for all · Esc close"
	}
	// Yoga measures the row text against the parent width before the
	// horizontal margin shrinks its final painted width. Preserve that
	// measured height when the second wrap needs an additional row.
	first := exportWrapWords(guide, max(1, width))
	measured := 1
	for _, line := range first {
		measured = max(measured, exportWidth(line))
	}
	footer := terminalMarkdownWrap(terminalPillStyle(guide, theme, "inactive", false, false), min(max(1, width-1), measured))
	if len(footer) > len(first) {
		footer = footer[:len(first)]
	}
	for _, line := range footer {
		out = append(out, " "+line)
	}
	return terminalMarkdownFinalRows(out)
}
