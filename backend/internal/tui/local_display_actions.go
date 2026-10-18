package tui

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamnavigation"
	"time"
)

func (s *State) handleLocalDisplayAction(name string, now time.Time) (bool, []action) {
	switch name {
	case "app:toggleTodos":
		if s.Teammates == nil {
			state := teamnavigation.New()
			s.Teammates = &state
		}
		if len(s.Teammates.Running()) > 0 {
			switch s.Teammates.ExpandedView {
			case "none":
				s.Teammates.ExpandedView = "tasks"
			case "tasks":
				s.Teammates.ExpandedView = "teammates"
			default:
				s.Teammates.ExpandedView = "none"
			}
		} else if s.Teammates.ExpandedView == "tasks" {
			s.Teammates.ExpandedView = "none"
		} else {
			s.Teammates.ExpandedView = "tasks"
		}
		if s.Teammates.ExpandedView == "tasks" {
			return true, []action{{kind: actionInvoke, name: "app:refreshTodos"}}
		}
		return true, nil
	case "app:toggleTeammatePreview":
		s.ShowTeammatePreview = !s.ShowTeammatePreview
		return true, nil
	case "chat:killAgents":
		running := false
		for _, property := range s.BackgroundTasks.OwnProperties() {
			task := presentationObject(property.Value)
			if task.String("type") == "local_agent" && task.String("status") == "running" {
				running = true
				break
			}
		}
		if !running {
			s.applyNotice(Event{Text: "No background agents running", NoticeTimeout: 2 * time.Second})
			return true, nil
		}
		previous, exists := s.lastPress[name]
		if exists && !now.Before(previous) && now.Sub(previous) <= 3*time.Second {
			delete(s.lastPress, name)
			s.Queue = nil
			s.Notification = ""
			return true, []action{{kind: actionInvoke, name: name}}
		}
		s.lastPress[name] = now
		shortcut, ok := BindingDisplay(name, "Chat", s.bindings)
		if !ok {
			shortcut = "ctrl+x ctrl+k"
		}
		s.applyNotice(Event{Text: "Press " + shortcut + " again to stop background agents", NoticeTimeout: 3 * time.Second})
		return true, nil
	}
	return false, nil
}
func (s *State) expandedTaskLines() []string { return s.actualExpandedTaskLines() }

func (s *State) setTaskItems(value []byte) {
	var items []protocol.JSONObject
	if json.Unmarshal(value, &items) != nil {
		return
	}
	s.TaskItems = items
	hidden := len(items) == 0
	if hidden && (!s.taskItemsHiddenKnown || !s.taskItemsHidden) && s.terminalExpandedView() == "tasks" {
		s.setTerminalExpandedView("none")
	}
	s.taskItemsHiddenKnown, s.taskItemsHidden = true, hidden
	allComplete := len(items) > 0
	for _, task := range items {
		if task.String("status") != "completed" {
			allComplete = false
		}
	}
	if allComplete {
		if s.taskItemsCompleteAt.IsZero() {
			s.taskItemsCompleteAt = s.dreamClock
			if s.taskItemsCompleteAt.IsZero() {
				s.taskItemsCompleteAt = time.Now()
			}
		}
	} else {
		s.taskItemsCompleteAt = time.Time{}
	}
}
