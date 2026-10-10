package queryhooks

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) ExecuteConfigChange(ctx context.Context, source string, filePath json.RawMessage, call Context) ([]OutsideResult, error) {
	switch source {
	case "user_settings", "project_settings", "local_settings", "policy_settings", "skills":
	default:
		return nil, unsupported("ConfigChange source " + source)
	}
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "permission_mode", Value: nil}, protocol.JSONField{Name: "agent_id", Value: nil}, protocol.JSONField{Name: "source", Value: commandJSON(source)}, protocol.JSONField{Name: "file_path", Value: filePath})
	results, err := s.ExecuteOutside(ctx, OutsideInput{Event: "ConfigChange", SessionID: s.mainSessionID(), MatchQuery: source, Fields: fields, Call: call})
	if err == nil && source == "policy_settings" {
		for i := range results {
			results[i].Blocked = false
		}
	}
	return results, err
}

func HasBlockingOutsideResult(results []OutsideResult) bool {
	for _, result := range results {
		if result.Blocked {
			return true
		}
	}
	return false
}
