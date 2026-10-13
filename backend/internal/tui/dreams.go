package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type dreamDialogState struct {
	taskID   string
	selected int
}

func (s *State) dreamTasks() []protocol.JSONObject {
	values := []protocol.JSONObject{}
	for _, property := range s.BackgroundTasks.OwnProperties() {
		var task protocol.JSONObject
		_ = task.UnmarshalJSON(property.Value)
		if task.String("type") == "dream" && (task.String("status") == "running" || task.String("status") == "pending") {
			values = append(values, task)
		}
	}
	sort.SliceStable(values, func(i, j int) bool {
		return tuiDreamNumber(values[i].Get("startTime")) > tuiDreamNumber(values[j].Get("startTime"))
	})
	return values
}
func tuiDreamNumber(raw json.RawMessage) float64 {
	var value float64
	_ = json.Unmarshal(raw, &value)
	return value
}
func (s *State) handleDreamKey(key Key) (bool, []action) {
	name := key.Name
	if name == "" {
		name = key.Text
	}
	if s.DreamDialog == nil {
		if name != "shift+down" && !(name == "down" && key.Stroke != nil && key.Stroke.Shift) {
			return false, nil
		}
		values := s.dreamTasks()
		if len(values) == 0 {
			return false, nil
		}
		s.DreamDialog = &dreamDialogState{}
		if len(values) == 1 {
			s.DreamDialog.taskID = values[0].String("id")
		}
		return true, nil
	}
	dialog := s.DreamDialog
	if name == "esc" || name == "escape" || name == "enter" && dialog.taskID != "" || name == " " || name == "space" || name == "y" && dialog.taskID != "" {
		s.DreamDialog = nil
		return true, nil
	}
	if name == "left" {
		if dialog.taskID != "" && len(s.dreamTasks()) > 1 {
			dialog.taskID = ""
		} else {
			s.DreamDialog = nil
		}
		return true, nil
	}
	values := s.dreamTasks()
	if dialog.taskID == "" {
		switch name {
		case "up":
			dialog.selected = max(0, dialog.selected-1)
		case "down":
			dialog.selected = min(max(0, len(values)-1), dialog.selected+1)
		case "enter":
			if len(values) > 0 {
				dialog.taskID = values[min(dialog.selected, len(values)-1)].String("id")
			}
		case "x":
			if len(values) > 0 {
				return true, []action{{kind: actionTeammate, teammate: TeammateAction{Kind: "stop-dream", TaskID: values[min(dialog.selected, len(values)-1)].String("id")}}}
			}
		}
		return true, nil
	}
	if name == "x" {
		task, _ := s.BackgroundTasks.Object(dialog.taskID)
		if task.String("status") == "running" {
			return true, []action{{kind: actionTeammate, teammate: TeammateAction{Kind: "stop-dream", TaskID: dialog.taskID}}}
		}
	}
	return true, nil
}
func pluralDream(n int, noun string) string {
	if n != 1 {
		return noun + "s"
	}
	return noun
}
func (s *State) dreamLines() []string {
	if s.DreamDialog == nil {
		return nil
	}
	dialog := s.DreamDialog
	if dialog.taskID == "" {
		lines := []string{"Background tasks"}
		for i, task := range s.dreamTasks() {
			prefix := "  "
			if i == dialog.selected {
				prefix = "> "
			}
			lines = append(lines, wrap(prefix+task.String("description")+" · "+task.String("status"), s.Width)...)
		}
		return append(lines, "↑/↓ select · Enter view · x stop · ←/Esc close")
	}
	task, ok := s.BackgroundTasks.Object(dialog.taskID)
	if !ok {
		return []string{"Memory consolidation", "(no text output)", "Esc/Enter/Space close"}
	}
	var touched []json.RawMessage
	_ = json.Unmarshal(task.Get("filesTouched"), &touched)
	n := int(tuiDreamNumber(task.Get("sessionsReviewing")))
	now := s.dreamClock
	if now.IsZero() {
		now = time.Now()
	}
	header := dreamDuration(float64(now.UnixMilli())-tuiDreamNumber(task.Get("startTime"))) + fmt.Sprintf(" · reviewing %d %s", n, pluralDream(n, "session"))
	if len(touched) > 0 {
		header += fmt.Sprintf(" · %d %s touched", len(touched), pluralDream(len(touched), "file"))
	}
	lines := []string{"Memory consolidation", header, "Status: " + task.String("status")}
	var turns []protocol.JSONObject
	_ = json.Unmarshal(task.Get("turns"), &turns)
	visible := []protocol.JSONObject{}
	for _, turn := range turns {
		if turn.String("text") != "" {
			visible = append(visible, turn)
		}
	}
	if len(visible) == 0 {
		label := "(no text output)"
		if task.String("status") == "running" {
			label = "Starting…"
		}
		lines = append(lines, label)
	} else {
		if len(visible) > 6 {
			hidden := len(visible) - 6
			lines = append(lines, fmt.Sprintf("(%d earlier %s)", hidden, pluralDream(hidden, "turn")))
			visible = visible[len(visible)-6:]
		}
		for _, turn := range visible {
			lines = append(lines, wrap(turn.String("text"), s.Width)...)
			count := int(tuiDreamNumber(turn.Get("toolUseCount")))
			if count > 0 {
				lines = append(lines, fmt.Sprintf("  (%d %s)", count, pluralDream(count, "tool")))
			}
		}
	}
	guide := "Esc/Enter/Space close"
	if task.String("status") == "running" {
		guide += " · x stop"
	}
	return append(lines, guide)
}
func (s *State) dreamFooterLines() []string {
	if len(s.dreamTasks()) == 0 {
		return nil
	}
	return wrap("dreaming · shift+down to view", s.Width)
}

func MemorySavedText(data protocol.JSONObject) string {
	var paths []json.RawMessage
	_ = json.Unmarshal(data.Get("writtenPaths"), &paths)
	verb := data.String("verb")
	if verb == "" {
		verb = "Saved"
	}
	parts := []string{verb + " " + fmt.Sprintf("%d %s", len(paths), map[bool]string{true: "memory", false: "memories"}[len(paths) == 1])}
	for _, path := range paths {
		var name string
		_ = json.Unmarshal(path, &name)
		parts = append(parts, name)
	}
	return strings.Join(parts, "\n")
}
