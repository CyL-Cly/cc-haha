package tui

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamnavigation"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

type TeammateAction struct{ Kind, TaskID, Message string }
type TeammateSession interface {
	SubscribeTeammates(context.Context, func(protocol.JSONObject)) (protocol.JSONObject, func(), error)
	SubscribeTeammateEvents(Emit) func()
	ControlTeammate(context.Context, TeammateAction) error
}

func (s *State) SetTeammateTasks(tasks protocol.JSONObject) {
	if s.Teammates == nil {
		state := teamnavigation.New()
		s.Teammates = &state
	}
	s.Teammates.Refresh(tasks)
}
func (s *State) handleTeammateKey(key Key) (bool, []action) {
	if s.Teammates == nil {
		return false, nil
	}
	name := key.Name
	if name == "" {
		name = key.Text
	}
	shift := strings.HasPrefix(name, "shift+")
	name = strings.TrimPrefix(name, "shift+")
	if name == "esc" {
		name = "escape"
	}
	if name == "enter" {
		name = "return"
	}
	if key.Stroke != nil {
		shift = shift || key.Stroke.Shift
	}
	handled, control := s.Teammates.Handle(teamnavigation.Key{Key: name, Shift: shift})
	if !handled {
		return false, nil
	}
	if control.Kind == "" {
		return true, nil
	}
	return true, []action{{kind: actionTeammate, teammate: TeammateAction{Kind: control.Kind, TaskID: control.TaskID}}}
}
func (s *State) teammateSubmit(input Input) ([]action, bool) {
	if s.Teammates == nil || s.Teammates.ViewSelectionMode != "viewing-agent" || s.Teammates.ViewingAgentTaskID == "" {
		return nil, false
	}
	task, ok := s.Teammates.Tasks.Object(s.Teammates.ViewingAgentTaskID)
	if !ok || task.String("type") != "in_process_teammate" {
		return nil, false
	}
	return []action{{kind: actionTeammate, teammate: TeammateAction{Kind: "send", TaskID: s.Teammates.ViewingAgentTaskID, Message: input.Text}}}, true
}
func (s *State) teammateTreeLines() []string {
	if s.Teammates == nil || s.Teammates.ExpandedView != "teammates" {
		return nil
	}
	lines := []string{}
	add := func(index int, label string) {
		prefix := "  "
		if s.Teammates.ViewSelectionMode == "selecting-agent" && s.Teammates.SelectedIPAgentIndex == index {
			prefix = "> "
		}
		lines = append(lines, wrap(prefix+label, s.Width)...)
	}
	add(-1, "Leader")
	running := s.Teammates.Running()
	for index, task := range running {
		identity := presentationObject(task.Get("identity"))
		activity := "working"
		if string(task.Get("isIdle")) == "true" {
			activity = "idle"
		}
		add(index, "@"+identity.String("agentName")+" · "+activity)
	}
	add(len(running), "Hide teammates")
	return lines
}

func (s *State) teammateInboxLines() []string {
	if s.Teammates != nil && s.Teammates.ViewSelectionMode == "viewing-agent" {
		return nil
	}
	lines := []string{}
	for _, row := range s.TeamInbox {
		if row.String("status") != "pending" || !teammateVisible(row) {
			continue
		}
		text := teammateDisplayText(row)
		lines = append(lines, wrap("@"+row.String("from")+" · "+text, s.Width)...)
	}
	return lines
}

func teammateDisplayText(row protocol.JSONObject) string {
	if summary := row.String("summary"); summary != "" {
		return summary
	}
	body := presentationObject([]byte(row.String("text")))
	switch body.String("type") {
	case "idle_notification":
		if summary := body.String("summary"); summary != "" {
			return "idle · " + summary
		}
		return "idle"
	case "teammate_terminated":
		return body.String("message")
	case "shutdown_approved":
		return "shutdown approved"
	case "plan_approval_request":
		return "plan approved"
	}
	return row.String("text")
}

func teammateTranscriptText(row protocol.JSONObject) string {
	row = row.Clone()
	row.Delete("summary")
	return teammateDisplayText(row)
}

func teammateVisible(row protocol.JSONObject) bool {
	if teamspawn.ShutdownApproved(row.String("text")) != nil {
		return false
	}
	body := presentationObject([]byte(row.String("text")))
	return body.String("type") != "idle_notification" && body.String("type") != "teammate_terminated"
}

func rawTeammatePresentation(row protocol.JSONObject) json.RawMessage {
	value, _ := row.MarshalJSON()
	return value
}
func (s *State) teammateTranscriptLines() ([]string, bool) {
	if s.Teammates == nil || s.Teammates.ViewSelectionMode != "viewing-agent" {
		return nil, false
	}
	task, ok := s.Teammates.Tasks.Object(s.Teammates.ViewingAgentTaskID)
	if !ok {
		return nil, false
	}
	identity := presentationObject(task.Get("identity"))
	lines := wrap("@"+identity.String("agentName")+" · "+task.String("description")+" · esc to return", s.Width)
	var messages []protocol.JSONObject
	_ = json.Unmarshal(task.Get("messages"), &messages)
	for _, message := range messages {
		kind := message.String("type")
		body := presentationObject(message.Get("message"))
		var blocks []protocol.JSONObject
		if json.Unmarshal(body.Get("content"), &blocks) == nil {
			for _, block := range blocks {
				switch block.String("type") {
				case "tool_use":
					name := block.String("name")
					lines = append(lines, wrap("… "+name+": "+toolInputPresentation(name, block.Get("input")), s.Width)...)
				case "tool_result":
					lines = append(lines, wrap("⎿ "+contentPresentation(block.Get("content")), s.Width)...)
				case "thinking":
					lines = append(lines, wrap("Thinking: "+block.String("thinking"), s.Width)...)
				}
			}
		}
		text := contentPresentation(body.Get("content"))
		if text == "" && message.Get("content") != nil {
			text = contentPresentation(message.Get("content"))
		}
		if text == "" {
			continue
		}
		label := kind + ": "
		if kind == "user" {
			label = "You: "
		} else if kind == "assistant" {
			label = "Claude: "
		}
		lines = append(lines, wrap(label+text, s.Width)...)
		lines = append(lines, "")
	}
	return lines, true
}
