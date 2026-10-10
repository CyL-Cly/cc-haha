package mcpauth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SaveClientSecretJSON is the same secure writer as SaveClientSecret. Native
// terminal input uses the raw string form so a UTF-16 slice does not lose an
// unpaired surrogate before the actual UTF-8 OAuth request boundary.
func (s *Service) SaveClientSecretJSON(ctx context.Context, name string, config protocol.JSONObject, secret json.RawMessage) error {
	if _, err := protocol.DecodeJavaScriptJSONString(secret); err != nil {
		return errors.New("MCP client secret requires a JSON string")
	}
	key, err := GetServerKey(name, config)
	if err != nil {
		return err
	}
	secret = append(json.RawMessage(nil), secret...)
	return s.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpOAuthClientConfig"))
		slots.Set(key, raw(protocol.NewJSONObject(protocol.JSONField{Name: "clientSecret", Value: secret})))
		data.Set("mcpOAuthClientConfig", raw(slots))
		return nil
	})
}
