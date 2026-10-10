package taskstore

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// UpdateAtomically executes Source's conditional task patch after the same
// lifecycle/list/file leases have re-read the actual task. A nil patch is the
// original undefined return, preserving a concurrent actor's reassignment.
func (s *Service) UpdateAtomically(list string, id json.RawMessage, derive func(protocol.JSONObject) (*protocol.JSONObject, error)) (*Mutation, error) {
	if derive == nil {
		return nil, fmt.Errorf("task atomic update requires callback")
	}
	return s.update(list, id, protocol.NewJSONObject(), true, derive)
}

type UnassignTasksResult struct {
	UnassignedTasks     []protocol.JSONObject `json:"unassignedTasks"`
	NotificationMessage string                `json:"notificationMessage"`
}

func (s *Service) UnassignTeammateTasks(team, id, name, reason string) (UnassignTasksResult, error) {
	result := UnassignTasksResult{UnassignedTasks: []protocol.JSONObject{}}
	tasks, err := s.List(team)
	if err != nil {
		return result, err
	}
	for _, task := range tasks {
		if task.String("status") == "completed" || task.String("owner") != id && task.String("owner") != name {
			continue
		}
		mutation, err := s.UpdateAtomically(team, task.Get("id"), func(current protocol.JSONObject) (*protocol.JSONObject, error) {
			if current.String("status") == "completed" || current.String("owner") != id && current.String("owner") != name {
				return nil, nil
			}
			patch := protocol.NewJSONObject(protocol.JSONField{Name: "owner"}, protocol.JSONField{Name: "status", Value: quote("pending")})
			return &patch, nil
		})
		if err != nil {
			return result, err
		}
		if mutation != nil && mutation.Updated {
			result.UnassignedTasks = append(result.UnassignedTasks, protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: mutation.Task.Get("id")}, protocol.JSONField{Name: "subject", Value: mutation.Task.Get("subject")}))
		}
	}
	verb := "has shut down"
	if reason == "terminated" {
		verb = "was terminated"
	}
	result.NotificationMessage = name + " " + verb + "."
	if len(result.UnassignedTasks) > 0 {
		items := make([]string, len(result.UnassignedTasks))
		for i, task := range result.UnassignedTasks {
			items[i] = "#" + task.String("id") + ` "` + task.String("subject") + `"`
		}
		result.NotificationMessage += fmt.Sprintf(" %d task(s) were unassigned: %s. Use TaskList to check availability and TaskUpdate with owner to reassign them to idle teammates.", len(items), strings.Join(items, ", "))
	}
	return result, nil
}
