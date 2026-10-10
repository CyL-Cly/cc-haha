package climigrations

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) mcp() error {
	if err := s.resolveProject(); err != nil {
		return err
	}
	project, err := s.options.Global.GetProject(s.projectKey)
	if err != nil {
		return err
	}
	hasEnable := project.Get("enableAllProjectMcpServers") != nil
	enabled, err := modelStringArray(project.Get("enabledMcpjsonServers"))
	if err != nil {
		return err
	}
	disabled, err := modelStringArray(project.Get("disabledMcpjsonServers"))
	if err != nil {
		return err
	}
	if !hasEnable && len(enabled) == 0 && len(disabled) == 0 {
		return nil
	}
	local, err := s.settings("localSettings")
	if err != nil {
		return s.caught(err, "", "tengu_migrate_mcp_approval_fields_error", map[string]any{})
	}
	patch := protocol.NewJSONObject()
	count := 0
	if hasEnable {
		count++
		if local.Get("enableAllProjectMcpServers") == nil {
			patch.Set("enableAllProjectMcpServers", project.Get("enableAllProjectMcpServers"))
		}
	}
	for _, field := range []struct {
		name   string
		values []json.RawMessage
	}{{"enabledMcpjsonServers", enabled}, {"disabledMcpjsonServers", disabled}} {
		if len(field.values) == 0 {
			continue
		}
		count++
		old, e := modelStringArray(local.Get(field.name))
		if e != nil {
			return s.caught(e, "", "tengu_migrate_mcp_approval_fields_error", map[string]any{})
		}
		merged := []json.RawMessage{}
		for _, value := range append(old, field.values...) {
			exists := false
			for _, previous := range merged {
				if bytes.Equal(previous, value) {
					exists = true
					break
				}
			}
			if !exists {
				merged = append(merged, value)
			}
		}
		encoded, _ := json.Marshal(merged)
		patch.Set(field.name, encoded)
	}
	if len(patch.OwnProperties()) > 0 {
		err = s.update("localSettings", patch)
	}
	if err == nil {
		err = s.saveProject(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Delete("enableAllProjectMcpServers")
			next.Delete("enabledMcpjsonServers")
			next.Delete("disabledMcpjsonServers")
			return &next, nil
		})
	}
	if err != nil {
		return s.caught(err, "", "tengu_migrate_mcp_approval_fields_error", map[string]any{})
	}
	s.event("tengu_migrate_mcp_approval_fields_success", map[string]any{"migratedCount": count})
	return nil
}
func modelStringArray(raw json.RawMessage) ([]json.RawMessage, error) {
	if !truthy(raw) {
		return nil, nil
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '[' {
		return nil, &CapabilityError{"MCP approval array shape"}
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, &CapabilityError{"MCP approval raw JSON"}
	}
	for index, value := range values {
		if len(bytes.TrimSpace(value)) == 0 || bytes.TrimSpace(value)[0] != '"' {
			return nil, &CapabilityError{"MCP approval non-string identity"}
		}
		values[index] = protocol.StringifyValidatedJavaScriptJSON(value)
	}
	return values, nil
}
