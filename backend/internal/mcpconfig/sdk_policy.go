package mcpconfig

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// FilterProcessInjection is the shared enterprise gate for mcp_set_servers.
// SDK-managed control transports keep the original policy exemption.
func (s *Service) FilterProcessInjection(ctx context.Context, servers protocol.JSONObject) (protocol.JSONObject, []string, error) {
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return protocol.JSONObject{}, nil, err
	}
	policy, err := s.options.Policy(ctx)
	if err != nil {
		return protocol.JSONObject{}, nil, err
	}
	allowed, blocked := protocol.NewJSONObject(), []string{}
	for _, field := range servers.OwnProperties() {
		config := object(field.Value)
		if config.String("type") == "sdk" || Allowed(name(field), config, settings, policy) {
			if name(field) != "__proto__" {
				_ = allowed.SetPropertyJSON(field.NameJSON, field.Value)
			}
		} else {
			blocked = append(blocked, name(field))
		}
	}
	return allowed, blocked, nil
}
