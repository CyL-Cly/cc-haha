package taskstore

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// UpdateTask is Source updateTask, distinct from the model tool's conditional
// update/metadata merge. It reuses the same physical lifecycle/list/task lease
// transaction and supports an own undefined property when clearing a field.
func (s *Service) UpdateTask(list string, id json.RawMessage, updates protocol.JSONObject) (*protocol.JSONObject, error) {
	result, err := s.update(list, id, updates, true)
	if err != nil || result == nil {
		return nil, err
	}
	task := result.Task.Clone()
	return &task, nil
}
