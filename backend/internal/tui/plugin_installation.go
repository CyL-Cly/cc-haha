package tui

import (
	"context"
	"encoding/json"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type TerminalPluginInstallationSource interface {
	SubscribeTerminalPluginInstallation(context.Context, func(json.RawMessage)) (json.RawMessage, func(), error)
}
type pluginInstallationChangedMsg struct{ status json.RawMessage }

func enqueueTerminalPluginInstallation(ctx context.Context, queue chan json.RawMessage, status json.RawMessage) {
	value := append(json.RawMessage(nil), status...)
	select {
	case <-ctx.Done():
		return
	default:
	}
	select {
	case queue <- value:
		return
	default:
	}
	select {
	case <-queue:
	default:
	}
	select {
	case queue <- value:
	case <-ctx.Done():
	default:
	}
}

func (m *terminalModel) terminalPluginInstallationCommand() tea.Cmd {
	if m.pluginInstallationChanges == nil {
		return nil
	}
	return func() tea.Msg {
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		select {
		case <-m.ctx.Done():
			return nil
		case value := <-m.pluginInstallationChanges:
			return pluginInstallationChangedMsg{value}
		}
	}
}

func (m *terminalModel) pluginInstallationChanged() []action {
	if m.state.LocalPanel == nil || m.state.LocalPanel.props.Kind != "plugin" {
		return nil
	}
	return []action{{kind: actionLocalPanel, panel: LocalPanelResult{ID: m.state.LocalPanel.props.ID, Action: "plugin-installation-refresh"}}}
}

type terminalPluginNotice struct {
	key, text string
	priority  int
}
type terminalPluginNotices struct {
	reloadPending, flaggedNotified bool
	current                        string
	queue                          []terminalPluginNotice
}

func (s *State) applyTerminalPluginInstallationStatus(data json.RawMessage, now time.Time) {
	var status protocol.JSONObject
	if status.UnmarshalJSON(data) != nil || !status.IsObject() {
		return
	}
	if s.pluginNotices == nil {
		s.pluginNotices = &terminalPluginNotices{}
	}
	state := s.pluginNotices
	pending := string(status.Get("needsRefresh")) == "true"
	if pending && !state.reloadPending {
		s.queueTerminalPluginNotice(terminalPluginNotice{"plugin-reload-pending", "Plugins changed. Run /reload-plugins to activate.", 0}, now)
	}
	state.reloadPending = pending
	var flagged []json.RawMessage
	_ = json.Unmarshal(status.Get("flaggedPlugins"), &flagged)
	if len(flagged) > 0 && !state.flaggedNotified {
		state.flaggedNotified = true
		s.queueTerminalPluginNotice(terminalPluginNotice{"plugin-delisted-flagged", "Plugins flagged. Check /plugins", 2}, now)
	}
}

func (s *State) queueTerminalPluginNotice(value terminalPluginNotice, now time.Time) {
	state := s.pluginNotices
	if state.current == value.key {
		return
	}
	for _, queued := range state.queue {
		if queued.key == value.key {
			return
		}
	}
	state.queue = append(state.queue, value)
	s.advanceTerminalPluginNotice(now)
}

func (s *State) advanceTerminalPluginNotice(now time.Time) {
	state := s.pluginNotices
	if state == nil || s.Notification != "" || len(state.queue) == 0 {
		return
	}
	index := 0
	for i := range state.queue {
		if state.queue[i].priority > state.queue[index].priority {
			index = i
		}
	}
	next := state.queue[index]
	state.queue = append(state.queue[:index], state.queue[index+1:]...)
	state.current = next.key
	s.Notification = next.text
	s.notificationUntil = now.Add(8 * time.Second)
}

func (s *State) terminalPluginNotificationLines() []string {
	lines := wrap(s.Notification, s.Width)
	if s.pluginNotices == nil || s.pluginNotices.current == "" {
		return lines
	}
	if s.pluginNotices.current == "immediate-btw" || s.pluginNotices.current == "ordinary-current" {
		return lines
	}
	role := "suggestion"
	if s.pluginNotices.current == "plugin-delisted-flagged" {
		role = "warning"
	}
	color := TerminalThemeRoleEscape(s.previewTheme(), role, false)
	for i, line := range lines {
		lines[i] = color + line + "\x1b[0m"
	}
	return lines
}
