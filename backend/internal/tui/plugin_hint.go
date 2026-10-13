package tui

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type PluginHint struct{ Name, Marketplace, Description, SourceCommand string }
type PluginHintSession interface {
	SubscribePluginHints(context.Context, func(*PluginHint)) (*PluginHint, func(), error)
	RespondPluginHint(context.Context, string) (string, error)
}
type pluginHintState struct {
	hint     PluginHint
	selected int
	expires  time.Time
}
type pluginHintChangedMsg struct{ hint *PluginHint }
type pluginHintResponseMsg struct {
	notice string
	err    error
}

func (s *State) pluginHintVisible() bool {
	return s.PluginHint != nil && !s.Closing && !s.Editing && !s.Searching && !s.Transcript && s.MessageSelector == nil && s.WorktreeExit == nil && s.ThinkingPicker == nil && s.ModelPicker == nil && len(s.Permissions) == 0 && !s.pluginHintPromptActive
}
func (s *State) pluginHintTick(now time.Time) []action {
	if !s.pluginHintTypingUntil.IsZero() && !now.Before(s.pluginHintTypingUntil) {
		s.pluginHintPromptActive = false
	}
	if s.PluginHint == nil {
		return nil
	}
	if !s.pluginHintVisible() {
		s.PluginHint.expires = time.Time{}
		return nil
	}
	if s.PluginHint.expires.IsZero() {
		s.PluginHint.expires = now.Add(30 * time.Second)
	} else if !now.Before(s.PluginHint.expires) {
		s.PluginHint = nil
		return []action{{kind: actionPluginHint, name: "no"}}
	}
	return nil
}
func (s *State) pluginHintInputChanged(now time.Time) {
	s.pluginHintPromptActive = strings.TrimFunc(s.Editor.Text(), protocol.IsJavaScriptWhitespace) != ""
	s.pluginHintTypingUntil = now.Add(1500 * time.Millisecond)
}
func (s *State) handlePluginHintKey(key Key) (bool, []action) {
	if !s.pluginHintVisible() {
		return false, nil
	}
	choice := ""
	resolved := ResolveChord(keyStroke(key), []string{"Select"}, s.bindings, nil)
	if resolved.Type == "match" {
		switch resolved.Action {
		case "select:previous":
			s.PluginHint.selected = (s.PluginHint.selected + 2) % 3
		case "select:next":
			s.PluginHint.selected = (s.PluginHint.selected + 1) % 3
		case "select:cancel":
			choice = "no"
		case "select:accept":
			choice = []string{"yes", "no", "disable"}[s.PluginHint.selected]
		}
	} else {
		name := key.Name
		if name == "" {
			name = key.Text
		}
		if len([]rune(name)) == 1 {
			r := []rune(name)[0]
			if r >= '１' && r <= '３' {
				name = string(r - '１' + '1')
			}
		}
		switch name {
		case "1":
			choice = "yes"
		case "2":
			choice = "no"
		case "3":
			choice = "disable"
		case "space", " ", "　":
			choice = []string{"yes", "no", "disable"}[s.PluginHint.selected]
		case "ctrl+c", "ctrl+d", "ctrl+l":
			return false, nil
		default:
			return true, nil
		}
	}
	if choice != "" {
		s.PluginHint = nil
		return true, []action{{kind: actionPluginHint, name: choice}}
	}
	return true, nil
}
func (h *pluginHintState) lines(width int) []string {
	lines := wrap("Plugin Recommendation", width)
	for _, text := range []string{"The " + h.hint.SourceCommand + " command suggests installing a plugin.", "Plugin: " + h.hint.Name, "Marketplace: " + h.hint.Marketplace, h.hint.Description, "Would you like to install it?"} {
		if text != "" {
			lines = append(lines, wrap(text, width)...)
		}
	}
	for i, text := range []string{"Yes, install " + h.hint.Name, "No", "No, and don't show plugin installation hints again"} {
		prefix := "  "
		if i == h.selected {
			prefix = "> "
		}
		lines = append(lines, wrap(prefix+text, width)...)
	}
	return lines
}
func (m *terminalModel) pluginHintCommand(response string) tea.Cmd {
	return func() tea.Msg {
		if !m.beginTurn() {
			return nil
		}
		defer m.endTurn()
		host, ok := m.session.(PluginHintSession)
		if !ok {
			return nil
		}
		notice, err := host.RespondPluginHint(m.ctx, response)
		return pluginHintResponseMsg{notice, err}
	}
}
func (m *terminalModel) bindPluginHints() (func(), error) {
	host, ok := m.session.(PluginHintSession)
	if !ok {
		return nil, nil
	}
	initial, unsubscribe, err := host.SubscribePluginHints(m.ctx, func(hint *PluginHint) { m.program.Send(pluginHintChangedMsg{hint}) })
	if initial != nil {
		m.state.PluginHint = &pluginHintState{hint: *initial}
	}
	return unsubscribe, err
}
