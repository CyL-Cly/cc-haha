package resumestate

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// InvokeSkill uses the same scoped key and replacement order as resume's
// restored skill map; its content remains original prompt text.
func (service *Service) InvokeSkill(name, path, content, agentID string) {
	_ = service.InvokeSkillJSON(quote(name), quote(path), content, agentID)
}

// InvokeSkillJSON retains original logical identities in the existing map rows.
func (service *Service) InvokeSkillJSON(name, path json.RawMessage, content, agentID string) error {
	if _, e := protocol.DecodeJavaScriptJSONString(name); e != nil {
		return e
	}
	if _, e := protocol.DecodeJavaScriptJSONString(path); e != nil {
		return e
	}
	key, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote(agentID + ":"), name}, "")
	if e != nil {
		return e
	}
	agent := []byte("null")
	if agentID != "" {
		agent = quote(agentID)
	}
	info := protocol.NewJSONObject(protocol.JSONField{Name: "skillName", Value: name}, protocol.JSONField{Name: "skillPath", Value: path}, protocol.JSONField{Name: "content", Value: quote(content)}, protocol.JSONField{Name: "invokedAt", Value: encode(service.options.Now())}, protocol.JSONField{Name: "agentId", Value: agent})
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "key", Value: key}, protocol.JSONField{Name: "value", Value: encode(info)})
	service.mu.Lock()
	defer service.mu.Unlock()
	for i, item := range service.state.Skills {
		if scalarEqual(item.Get("key"), key) {
			service.state.Skills[i] = entry
			return nil
		}
	}
	service.state.Skills = append(service.state.Skills, entry)
	return nil
}
