package adapterconfig

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These producer-only fields are absent from the public desktop PUT schema.
func (s *Service) UpdateWhatsAppBinding(ctx context.Context, section protocol.JSONObject) error {
	return s.update(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "whatsapp", Value: adapterJSON(section)}))
}

func (s *Service) UnbindWhatsApp(ctx context.Context, defaultDir string) error {
	section := protocol.NewJSONObject(protocol.JSONField{Name: "authDir", Value: adapterJSON(defaultDir)}, protocol.JSONField{Name: "pairedUsers", Value: adapterJSON([]any{})}, protocol.JSONField{Name: "allowedUsers", Value: adapterJSON([]any{})})
	return s.update(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "whatsapp", Value: adapterJSON(section)}), [2]string{"whatsapp", "accountJid"})
}
