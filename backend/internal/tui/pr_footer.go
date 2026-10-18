package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type TerminalPRStatusSession interface {
	FetchTerminalPRStatus(context.Context) (json.RawMessage, error)
}

type terminalPRFooterRuntime struct {
	generation                           uint64
	disabled, loading, enabled, fetching bool
	lastFetch, nextPoll, lastActivity    time.Time
	interaction, lastSeenInteraction     uint64
}

type terminalPRFooterResultMsg struct {
	generation         uint64
	started, completed time.Time
	value              json.RawMessage
}

// All hook state is reduced on the terminal event loop. A native fetch returns
// a detached message; an effect restart ignores its late result, as React's
// cancelled closure does, without aborting that independent gh invocation.
func (m *terminalModel) terminalPRFooterRestart(now time.Time) tea.Cmd {
	if m.terminalPRFooter == nil {
		m.terminalPRFooter = &terminalPRFooterRuntime{}
	}
	r := m.terminalPRFooter
	r.generation++
	r.enabled, r.loading = m.state.Preferences.PRStatusFooterEnabled, m.state.Busy()
	r.fetching = false
	r.nextPoll = time.Time{}
	r.lastActivity = now
	r.lastSeenInteraction = ^uint64(0)
	if !r.enabled || r.disabled {
		return nil
	}
	r.nextPoll = r.lastFetch.Add(time.Minute)
	return m.terminalPRFooterTick(now)
}

func (m *terminalModel) terminalPRFooterInteraction() {
	if m.terminalPRFooter != nil {
		m.terminalPRFooter.interaction++
	}
}

func (m *terminalModel) terminalPRFooterSync(now time.Time) tea.Cmd {
	r := m.terminalPRFooter
	if r != nil && (r.enabled != m.state.Preferences.PRStatusFooterEnabled || r.loading != m.state.Busy()) {
		return m.terminalPRFooterRestart(now)
	}
	return nil
}

func (m *terminalModel) terminalPRFooterTick(now time.Time) tea.Cmd {
	r := m.terminalPRFooter
	if r == nil {
		return m.terminalPRFooterRestart(now)
	}
	if r.enabled != m.state.Preferences.PRStatusFooterEnabled || r.loading != m.state.Busy() {
		return m.terminalPRFooterRestart(now)
	}
	if !r.enabled || r.disabled || r.fetching || r.nextPoll.IsZero() || now.Before(r.nextPoll) {
		return nil
	}
	if r.lastSeenInteraction != r.interaction {
		r.lastSeenInteraction, r.lastActivity = r.interaction, now
	} else if now.Sub(r.lastActivity) >= time.Hour {
		r.nextPoll = time.Time{}
		return nil
	}
	r.fetching = true
	r.nextPoll = time.Time{}
	return m.terminalPRFooterCommand()
}

func (m *terminalModel) terminalPRFooterCommand() tea.Cmd {
	host, ok := m.session.(TerminalPRStatusSession)
	if !ok || m.terminalPRFooter == nil {
		return nil
	}
	generation := m.terminalPRFooter.generation
	return func() tea.Msg {
		// Like the updater, this is joined on final shutdown, without keeping
		// the foreground drain alive while worktree exit awaits cancellation.
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		start := time.Now()
		value, _ := host.FetchTerminalPRStatus(m.ctx)
		return terminalPRFooterResultMsg{generation: generation, started: start, completed: time.Now(), value: append(json.RawMessage(nil), value...)}
	}
}

func (m *terminalModel) terminalPRFooterResult(msg terminalPRFooterResultMsg) tea.Cmd {
	r := m.terminalPRFooter
	if r == nil || r.generation != msg.generation || !r.enabled {
		return nil
	}
	r.fetching = false
	r.lastFetch = msg.started
	m.state.applyTerminalPRStatusAt(msg.value, msg.completed)
	if msg.completed.Sub(msg.started) > 4*time.Second {
		r.disabled = true
		r.nextPoll = time.Time{}
	} else {
		r.nextPoll = msg.completed.Add(time.Minute)
	}
	return nil
}

func (s *State) applyTerminalPRStatus(raw []byte) {
	s.applyTerminalPRStatusAt(raw, time.Now())
}

func (s *State) applyTerminalPRStatusAt(raw []byte, now time.Time) {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(raw)
	number, review := value.Get("number"), value.Get("reviewState")
	if len(number) == 0 {
		number = json.RawMessage("null")
	}
	if len(review) == 0 {
		review = json.RawMessage("null")
	}
	previousNumber, previousReview := s.TerminalPRStatus.Get("number"), s.TerminalPRStatus.Get("reviewState")
	if len(previousNumber) == 0 {
		previousNumber = json.RawMessage("null")
	}
	if len(previousReview) == 0 {
		previousReview = json.RawMessage("null")
	}
	if string(previousNumber) == string(number) && string(previousReview) == string(review) {
		return
	}
	url := value.Get("url")
	if len(url) == 0 {
		url = json.RawMessage("null")
	}
	s.TerminalPRStatus = protocol.NewJSONObject(protocol.JSONField{Name: "number", Value: number}, protocol.JSONField{Name: "url", Value: url}, protocol.JSONField{Name: "reviewState", Value: review}, protocol.JSONField{Name: "lastUpdated", Value: json.RawMessage(fmt.Sprint(now.UnixMilli()))})
}

func terminalPRStatusVisible(enabled bool, number, review, url json.RawMessage, primaryItems, columns int) bool {
	present := func(value json.RawMessage) bool { return len(value) != 0 && string(value) != "null" }
	return enabled && present(number) && present(review) && present(url) && primaryItems < 2 && (primaryItems == 0 || columns >= 80)
}

func (s *State) terminalPRFooterLines() []string {
	primary := 0
	if s.PermissionMode != "" && s.PermissionMode != "default" {
		primary++
	}
	background := false
	for _, field := range s.BackgroundTasks.OwnProperties() {
		var task protocol.JSONObject
		if task.UnmarshalJSON(field.Value) == nil && (task.String("status") == "running" || task.String("status") == "pending") && string(task.Get("isBackgrounded")) != "false" {
			background = true
		}
	}
	viewingTeammate := false
	if s.Teammates != nil && s.Teammates.ViewSelectionMode == "viewing-agent" {
		if task, ok := s.Teammates.Tasks.Object(s.Teammates.ViewingAgentTaskID); ok {
			viewingTeammate = task.String("type") == "in_process_teammate"
		}
	}
	if background || viewingTeammate {
		primary++
	}
	if s.paneTeam {
		primary++
	}
	if !terminalPRStatusVisible(s.Preferences.PRStatusFooterEnabled, s.TerminalPRStatus.Get("number"), s.TerminalPRStatus.Get("reviewState"), s.TerminalPRStatus.Get("url"), primary, s.Width) || s.Mode == "bash" {
		return nil
	}
	var number any
	_ = json.Unmarshal(s.TerminalPRStatus.Get("number"), &number)
	text := "#" + fmt.Sprint(number)
	url := s.TerminalPRStatus.String("url")
	role := map[string]string{"approved": "success", "changes_requested": "error", "pending": "warning", "merged": "merged"}[s.TerminalPRStatus.String("reviewState")]
	color := terminalPRThemeRole(s.previewTheme(), role)
	if role == "" {
		text = "\x1b[2m" + text + "\x1b[22m"
	}
	text = color + text + "\x1b[39m"
	if url != "" {
		text = "\x1b]8;;" + displayText(url) + "\x1b\\\x1b[4m" + text + "\x1b[24m\x1b]8;;\x1b\\"
	}
	return []string{"\x1b[2mPR\x1b[22m " + text}
}

func terminalPRThemeRole(theme, role string) string {
	roles := map[string]map[string]string{
		"light":            {"success": "rgb(44,122,57)", "error": "rgb(171,43,63)", "warning": "rgb(150,108,30)", "merged": "rgb(135,0,255)"},
		"light-ansi":       {"success": "ansi:green", "error": "ansi:red", "warning": "ansi:yellow", "merged": "ansi:magenta"},
		"dark-ansi":        {"success": "ansi:greenBright", "error": "ansi:redBright", "warning": "ansi:yellowBright", "merged": "ansi:magentaBright"},
		"light-daltonized": {"success": "rgb(0,102,153)", "error": "rgb(204,0,0)", "warning": "rgb(255,153,0)", "merged": "rgb(135,0,255)"},
		"dark":             {"success": "rgb(78,186,101)", "error": "rgb(255,107,128)", "warning": "rgb(255,193,7)", "merged": "rgb(175,135,255)"},
		"dark-daltonized":  {"success": "rgb(51,153,255)", "error": "rgb(255,102,102)", "warning": "rgb(255,204,0)", "merged": "rgb(175,135,255)"},
	}
	values := roles[theme]
	if values == nil {
		values = roles["dark"]
	}
	return terminalThemeColorEscape(values[role], false)
}
