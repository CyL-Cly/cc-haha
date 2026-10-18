package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalVisibleBackgroundTask(value protocol.JSONObject) bool {
	status := value.String("status")
	return (status == "running" || status == "pending") && string(value.Get("isBackgrounded")) != "false"
}

func (s *State) terminalTasksFooterVisible() bool {
	running := false
	hide, visible := s.Teammates != nil && s.Teammates.ExpandedView == "teammates", false
	for _, property := range s.BackgroundTasks.OwnProperties() {
		task := presentationObject(property.Value)
		running = running || task.String("status") == "running"
		if terminalVisibleBackgroundTask(task) {
			visible = true
			if task.String("type") != "in_process_teammate" {
				hide = false
			}
		}
	}
	return running && !(hide && visible)
}

func (s *State) terminalFooterItems() []string {
	items := []string{}
	if s.terminalTasksFooterVisible() {
		items = append(items, "tasks")
	}
	// The frozen distribution's tmux (ant-only) and bagel getters are false.
	// Teams come from teamContext; CCR remote state is not the bridge pill.
	if len(s.CachedTeams) > 0 {
		items = append(items, "teams")
	}
	if s.terminalBuddyFooterVisible() {
		items = append(items, "companion")
	}
	return items
}

func (s *State) selectedTerminalFooter() string {
	selection := s.footerSelection
	if selection == "" && s.buddy != nil && s.buddy.focused {
		selection = "companion"
	}
	if !slices.Contains(s.terminalFooterItems(), selection) {
		s.selectTerminalFooter("")
		return ""
	}
	return selection
}

func (s *State) selectTerminalFooter(value string) {
	s.footerSelection = value
	if s.buddy != nil {
		s.buddy.focused = value == "companion"
	}
	if value == "tasks" {
		s.footerTeammateIndex = 0
	}
}

func (s *State) navigateTerminalFooter(delta int, exitAtStart bool) {
	items := s.terminalFooterItems()
	index := slices.Index(items, s.selectedTerminalFooter()) + delta
	if index >= 0 && index < len(items) {
		s.selectTerminalFooter(items[index])
	} else if delta < 0 && exitAtStart {
		s.selectTerminalFooter("")
	}
}

func (s *State) terminalFooterTeammateMode() bool {
	if s.Teammates == nil {
		return false
	}
	if len(s.Teammates.Running()) > 0 {
		return true
	}
	if s.Teammates.ViewSelectionMode == "viewing-agent" {
		value, present := s.BackgroundTasks.Object(s.Teammates.ViewingAgentTaskID)
		return present && value.String("type") == "in_process_teammate"
	}
	return false
}

func (s *State) terminalFooterOverlayActive() bool {
	return s.HelpOpen || s.LocalPanel != nil || s.Transcript || s.Searching || s.Closing || s.Editing || s.Doctor != nil || s.DreamDialog != nil || s.MessageSelector != nil || s.WorktreeExit != nil || s.ModelPicker != nil || s.ThinkingPicker != nil || s.IDEPicker != nil || s.ideOnboardingVisible() || s.pluginHintVisible() || len(s.Permissions) > 0
}

func (s *State) handleTerminalFooterKey(key Key, now time.Time) (bool, []action) {
	if s.terminalFooterOverlayActive() {
		return false, nil
	}
	selection := s.selectedTerminalFooter()
	if selection == "" {
		items := s.terminalFooterItems()
		if len(items) == 0 || (key.Name != "down" && key.Name != "ctrl+n") || len(s.Completions) > 1 || s.historyIndex != 0 || strings.Contains(s.Editor.Text()[s.Editor.Cursor():], "\n") {
			return false, nil
		}
		s.selectTerminalFooter(items[0])
		s.pendingChord = nil
		if items[0] == "tasks" {
			return true, []action{{kind: actionInvoke, name: "app:markTasksHint"}}
		}
		return true, nil
	}
	resolution := ResolveChord(keyStroke(key), []string{"Global", "Footer"}, s.bindings, s.pendingChord)
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
		case "footer:up":
			s.navigateTerminalFooter(-1, true)
			return true, nil
		case "footer:clearSelection":
			s.selectTerminalFooter("")
			return true, nil
		case "footer:down":
			if selection == "tasks" && !s.terminalFooterTeammateMode() {
				s.selectTerminalFooter("")
				return true, []action{{kind: actionInvoke, name: "command:tasks"}}
			}
			s.navigateTerminalFooter(1, false)
			return true, nil
		case "footer:next", "footer:previous":
			delta := 1
			if resolution.Action == "footer:previous" {
				delta = -1
			}
			if selection == "tasks" && s.terminalFooterTeammateMode() {
				count := 1 + len(s.Teammates.Running())
				s.footerTeammateIndex = (s.footerTeammateIndex + delta + count) % count
			} else {
				s.navigateTerminalFooter(delta, false)
			}
			return true, nil
		case "footer:openSelected":
			if s.Teammates != nil && s.Teammates.ViewSelectionMode == "selecting-agent" {
				return true, nil
			}
			switch selection {
			case "tasks":
				if s.terminalFooterTeammateMode() {
					if s.footerTeammateIndex == 0 {
						s.Teammates.Exit()
						return true, []action{{kind: actionTeammate, teammate: TeammateAction{Kind: "exit"}}}
					}
					members := s.Teammates.Running()
					index := s.footerTeammateIndex - 1
					if index < len(members) {
						id := members[index].String("id")
						s.Teammates.ViewSelectionMode, s.Teammates.ViewingAgentTaskID = "viewing-agent", id
						return true, []action{{kind: actionTeammate, teammate: TeammateAction{Kind: "enter", TaskID: id}}}
					}
					return true, nil
				}
				s.selectTerminalFooter("")
				return true, []action{{kind: actionInvoke, name: "command:tasks"}}
			case "companion":
				s.selectTerminalFooter("")
				s.Editor.Set("/buddy")
				s.Mode = "prompt"
				return true, s.submit()
			case "teams":
				// The real Teams mount is supplied by the optional Session consumer.
				s.selectTerminalFooter("")
				return true, []action{{kind: actionTerminalTeams}}
			}
		case "footer:close":
			// The frozen external profile never selects coordinator agent rows.
			// Returning to the ordinary printable path matches the false handler.
		default:
			return false, nil
		}
	}
	if key.Text != "" && (key.Stroke == nil || !key.Stroke.Ctrl && !key.Stroke.Meta && !key.Stroke.Alt) {
		s.selectTerminalFooter("")
		s.Editor.Insert(key.Text)
		s.updateCompletions()
		return true, nil
	}
	if key.Name == "ctrl+c" || key.Name == "ctrl+d" {
		return false, nil
	}
	return true, nil
}
