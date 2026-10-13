package resumestate

import (
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ResumeMessageNotFoundError is the original loader's distinguished boundary
// error. Public print mode emits it directly instead of adding the ordinary
// resume failure prefix; other load failures retain that prefix.
type ResumeMessageNotFoundError struct{ MessageUUID string }

func (e *ResumeMessageNotFoundError) Error() string {
	return fmt.Sprintf("No message found with message.uuid of: %s", e.MessageUUID)
}

func truncatePersistedHistory(rows []json.RawMessage, boundary string) ([]json.RawMessage, error) {
	if boundary == "" {
		return rows, nil
	}
	for index, raw := range rows {
		var row protocol.JSONObject
		if row.UnmarshalJSON(raw) == nil && row.String("uuid") == boundary {
			return rows[:index+1], nil
		}
	}
	return nil, &ResumeMessageNotFoundError{MessageUUID: boundary}
}
