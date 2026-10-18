package tui

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type TerminalTaskListSource interface {
	SubscribeTerminalTaskList(context.Context, func(json.RawMessage)) (json.RawMessage, func(), error)
}

type terminalTaskListChangedMsg struct{ value json.RawMessage }

func (m *terminalModel) terminalTaskListCommand() tea.Cmd {
	if m.taskListChanges == nil {
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
		case value := <-m.taskListChanges:
			return terminalTaskListChangedMsg{value}
		}
	}
}

func (s *State) applyTerminalTaskList(raw json.RawMessage) {
	value := presentationObject(raw)
	s.taskTeamContext = presentationObject(value.Get("teamContext"))
	s.taskTeamsEnabled = string(value.Get("teamsEnabled")) == "true"
	var revision uint64
	_ = json.Unmarshal(value.Get("expandedViewRevision"), &revision)
	if revision > s.taskExpandedViewRevision {
		s.taskExpandedViewRevision = revision
		s.setTerminalExpandedView(value.String("expandedView"))
	}
	s.setTaskItems(value.Get("tasks"))
}

func (s *State) taskListLocalJSXMounted() bool {
	// These native projections retain their actual command caller. The model
	// shortcut is PromptInput's own picker, while /model is REPL local JSX.
	// IDE onboarding lives in IDEOnboarding and is not a slash command.
	if s.ModelPicker != nil && s.ModelPicker.props.Standalone || s.Doctor != nil || s.IDEPicker != nil {
		return true
	}
	p := s.LocalPanel
	if p == nil {
		return false
	}
	if p.props.OriginKnown {
		return p.props.LocalJSXCommand
	}
	// Existing direct internal projections predate the origin carrier. Teams
	// is the actual PromptInput early-return caller, not a slash JSX command.
	return p.props.Kind != "teams"
}

func (s *State) terminalTaskListMount() string {
	if s.Closing || s.Transcript || s.terminalExpandedView() != "tasks" || len(s.TaskItems) == 0 {
		return ""
	}
	localJSX := s.taskListLocalJSXMounted()
	showSpinner := !localJSX && len(s.Permissions) == 0 && s.question == nil && !s.taskListStreaming && (s.Busy() || len(s.Queue) > 0 || s.Teammates != nil && len(s.Teammates.Running()) > 0)
	if showSpinner {
		// Spinner's idle-leader/viewed-idle branches have no TaskListV2.
		if s.ActiveTurn == "" && s.Teammates != nil && len(s.Teammates.Running()) > 0 {
			return ""
		}
		return "spinner"
	}
	if localJSX {
		return ""
	}
	return "standalone"
}

func (s *State) syncTerminalTaskListMount() {
	mount := s.terminalTaskListMount()
	if mount != s.taskListMount {
		s.taskListMount = mount
		s.taskListRenderer = nil
	}
}

func (s *State) actualExpandedTaskLines() []string {
	s.syncTerminalTaskListMount()
	if s.taskListMount == "" {
		return nil
	}
	if s.taskListRenderer == nil {
		s.taskListRenderer = &terminalTaskListRenderState{}
	}
	theme := s.previewTheme()
	if theme == "auto" {
		theme = "dark"
		if s.LightBackground {
			theme = "light"
		}
	}
	now := s.dreamClock
	if now.IsZero() {
		now = time.Now()
	}
	standalone := s.taskListMount == "standalone"
	available := s.Width
	if !standalone {
		available = max(1, available-5)
	}
	rows := s.taskListRenderer.rows(s.TaskItems, s.BackgroundTasks, s.taskTeamContext, s.taskTeamsEnabled, standalone, s.Width, s.Height, theme, now, available)
	if !standalone {
		if len(rows) == 0 {
			rows = []string{""}
		}
		for index := range rows {
			prefix := strings.Repeat(" ", 5)
			if index == 0 {
				prefix = terminalTaskText("  ⎿ \u00a0", theme, "inactive", false, false)
			}
			rows[index] = prefix + rows[index]
		}
	}
	return rows
}
