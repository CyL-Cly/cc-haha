package server

import (
	"encoding/json"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Server) whatsappRegistrationHTTP(w http.ResponseWriter, r *http.Request, tail []string, convert func(error) error) error {
	service := s.options.WhatsAppRegistration
	if service == nil {
		return unsupported("adapters/whatsapp platform account producer")
	}
	if r.Method == http.MethodPost && len(tail) > 1 && tail[0] == "login" {
		switch tail[1] {
		case "start":
			result, err := service.Begin(s.ctx)
			if err != nil {
				return convert(err)
			}
			writeJSON(w, 200, result)
			return nil
		case "poll":
			key, err := readFeishuSessionKey(r)
			if err != nil {
				return err
			}
			var text string
			_ = json.Unmarshal(key, &text)
			result, err := service.Poll(text)
			if err != nil {
				return convert(err)
			}
			if string(result.Get("connected")) == "true" {
				binding := protocol.NewJSONObject(protocol.JSONField{Name: "accountJid", Value: result.Get("accountJid")}, protocol.JSONField{Name: "authDir", Value: result.Get("authDir")}, protocol.JSONField{Name: "pairedUsers", Value: json.RawMessage(`[]`)})
				if err = s.options.Adapters.UpdateWhatsAppBinding(s.ctx, binding); err != nil {
					return convert(err)
				}
				data, err := s.options.Adapters.MaskedJSON(s.ctx)
				if err != nil {
					return convert(err)
				}
				writeJSON(w, 200, json.RawMessage(data))
				return nil
			}
			writeJSON(w, 200, result)
			return nil
		}
	}
	if r.Method == http.MethodPost && len(tail) > 0 && tail[0] == "unbind" {
		if err := service.Unbind(); err != nil {
			return convert(err)
		}
		if err := s.options.Adapters.UnbindWhatsApp(s.ctx, service.DefaultAuthDir()); err != nil {
			return convert(err)
		}
		data, err := s.options.Adapters.MaskedJSON(s.ctx)
		if err != nil {
			return convert(err)
		}
		writeJSON(w, 200, json.RawMessage(data))
		return nil
	}
	return &apiError{404, "NOT_FOUND", "Unknown WhatsApp adapter endpoint"}
}
