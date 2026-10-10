// Package teamnavigation ports the original teammate tree and transcript view
// transitions. Execution actions remain with the actual owning task controller.
package teamnavigation

import (
	"encoding/json"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type State struct {
	Tasks                protocol.JSONObject `json:"tasks"`
	ExpandedView         string              `json:"expandedView"`
	ViewSelectionMode    string              `json:"viewSelectionMode"`
	SelectedIPAgentIndex int                 `json:"selectedIPAgentIndex"`
	ViewingAgentTaskID   string              `json:"viewingAgentTaskId,omitempty"`
}
type Key struct {
	Key   string `json:"key"`
	Shift bool   `json:"shift,omitempty"`
}
type Action struct{ Kind, TaskID string }

func New() State {
	return State{Tasks: protocol.NewJSONObject(), ExpandedView: "none", ViewSelectionMode: "none", SelectedIPAgentIndex: -1}
}
func object(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(raw)
	return value
}
func isTeammate(task protocol.JSONObject) bool { return task.String("type") == "in_process_teammate" }
func (s *State) Running() []protocol.JSONObject {
	values := []protocol.JSONObject{}
	for _, property := range s.Tasks.OwnProperties() {
		task := object(property.Value)
		if isTeammate(task) && task.String("status") == "running" {
			values = append(values, task)
		}
	}
	comparer := collate.New(language.English)
	sort.SliceStable(values, func(i, j int) bool {
		return comparer.CompareString(object(values[i].Get("identity")).String("agentName"), object(values[j].Get("identity")).String("agentName")) < 0
	})
	return values
}
func (s *State) Exit() { s.ViewingAgentTaskID = ""; s.ViewSelectionMode = "none" }
func (s *State) Refresh(tasks protocol.JSONObject) {
	previous := len(s.Running())
	s.Tasks = tasks.Clone()
	count := len(s.Running())
	if count == 0 && previous > 0 && s.SelectedIPAgentIndex != -1 {
		s.SelectedIPAgentIndex = -1
		if s.ViewSelectionMode != "viewing-agent" {
			s.ViewSelectionMode = "none"
		}
	}
	maximum := count - 1
	if s.ExpandedView == "teammates" {
		maximum = count
	}
	if count > 0 && s.SelectedIPAgentIndex > maximum {
		s.SelectedIPAgentIndex = maximum
	}
	if s.ViewingAgentTaskID != "" {
		task, exists := s.Tasks.Object(s.ViewingAgentTaskID)
		if !exists {
			s.Exit()
			return
		}
		if isTeammate(task) {
			status := task.String("status")
			if status == "killed" || status == "failed" || truthy(task.Get("error")) || status != "running" && status != "completed" && status != "pending" {
				s.Exit()
			}
		}
	}
}
func truthy(value json.RawMessage) bool {
	if len(value) == 0 || string(value) == "null" || string(value) == "false" || string(value) == "0" || string(value) == `""` {
		return false
	}
	return true
}
func (s *State) Handle(key Key) (bool, Action) {
	running := s.Running()
	count := len(running)
	if key.Key == "escape" && s.ViewSelectionMode == "viewing-agent" {
		if task, ok := s.Tasks.Object(s.ViewingAgentTaskID); ok && isTeammate(task) && task.String("status") == "running" {
			return true, Action{"interrupt", s.ViewingAgentTaskID}
		}
		s.Exit()
		return true, Action{"exit", ""}
	}
	if key.Key == "escape" && s.ViewSelectionMode == "selecting-agent" {
		s.ViewSelectionMode = "none"
		s.SelectedIPAgentIndex = -1
		return true, Action{}
	}
	if key.Shift && (key.Key == "up" || key.Key == "down") {
		if count == 0 {
			for _, property := range s.Tasks.OwnProperties() {
				task := object(property.Value)
				if !isTeammate(task) && (task.String("status") == "running" || task.String("status") == "pending") && string(task.Get("isBackgrounded")) != "false" {
					return true, Action{"background-dialog", ""}
				}
			}
			return true, Action{}
		}
		if s.ExpandedView != "teammates" {
			s.ExpandedView = "teammates"
			s.ViewSelectionMode = "selecting-agent"
			s.SelectedIPAgentIndex = -1
			return true, Action{}
		}
		current := s.SelectedIPAgentIndex
		if key.Key == "down" {
			if current >= count {
				current = -1
			} else {
				current++
			}
		} else {
			if current <= -1 {
				current = count
			} else {
				current--
			}
		}
		s.SelectedIPAgentIndex = current
		s.ViewSelectionMode = "selecting-agent"
		return true, Action{}
	}
	selected := func() *protocol.JSONObject {
		if s.SelectedIPAgentIndex < 0 || s.SelectedIPAgentIndex >= count {
			return nil
		}
		return &running[s.SelectedIPAgentIndex]
	}
	if key.Key == "f" && s.ViewSelectionMode == "selecting-agent" && count > 0 {
		if task := selected(); task != nil {
			s.ViewSelectionMode = "viewing-agent"
			s.ViewingAgentTaskID = task.String("id")
			return true, Action{"enter", s.ViewingAgentTaskID}
		}
		return true, Action{}
	}
	if key.Key == "return" && s.ViewSelectionMode == "selecting-agent" {
		if s.SelectedIPAgentIndex == -1 {
			s.Exit()
			return true, Action{"exit", ""}
		}
		if s.SelectedIPAgentIndex >= count {
			s.ExpandedView = "none"
			s.ViewSelectionMode = "none"
			s.SelectedIPAgentIndex = -1
			return true, Action{}
		}
		if task := selected(); task != nil {
			s.ViewSelectionMode = "viewing-agent"
			s.ViewingAgentTaskID = task.String("id")
			return true, Action{"enter", s.ViewingAgentTaskID}
		}
		return true, Action{}
	}
	if key.Key == "k" && s.ViewSelectionMode == "selecting-agent" && s.SelectedIPAgentIndex >= 0 {
		if task := selected(); task != nil {
			return true, Action{"kill", task.String("id")}
		}
		return true, Action{}
	}
	return false, Action{}
}
