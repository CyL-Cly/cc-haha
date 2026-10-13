package taskstore

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ExecutionBlocked is a normal TaskUpdate refusal, not a transport failure.
type ExecutionBlocked struct{ Message string }

func (e ExecutionBlocked) Error() string { return e.Message }

// CheckTeammateExecution checks the existing dependencies. A deleted dependency
// is absent from the unresolved set, matching the original TaskUpdate contract.
func (s *Service) CheckTeammateExecution(list string, current protocol.JSONObject, input protocol.JSONObject) error {
	status := input.String("status")
	if (status != "in_progress" && status != "completed") || status == current.String("status") {
		return nil
	}
	dependencies := stringsFromJSON(current.Get("blockedBy"))
	waiting := []string{}
	for _, id := range dependencies {
		dependency, err := s.Get(list, id)
		if err != nil {
			return err
		}
		if dependency != nil && dependency.String("status") != "completed" {
			waiting = append(waiting, "#"+dependency.String("id"))
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	return ExecutionBlocked{fmt.Sprintf("Task #%s waits on %s, not completed yet. Do not work on it or change its status until then; TaskList shows which of your tasks are ready.", current.String("id"), strings.Join(waiting, ", "))}
}

// UpdateForTeammate rechecks after completion hooks under the list transaction,
// and fills only an unowned task. The lead continues to use ordinary Update.
func (s *Service) UpdateForTeammate(list string, id json.RawMessage, input protocol.JSONObject, member string) (*Mutation, error) {
	return s.update(list, id, input, false, func(current protocol.JSONObject) (*protocol.JSONObject, error) {
		if err := s.CheckTeammateExecution(list, current, input); err != nil {
			return nil, err
		}
		patch := input.Clone()
		status := input.String("status")
		if input.Get("owner") == nil && member != "" && current.String("owner") == "" && (status == "in_progress" || status == "completed") {
			patch.Set("owner", quote(member))
		}
		return &patch, nil
	})
}
