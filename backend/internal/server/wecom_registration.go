package server

import (
	"encoding/json"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Server) wecomRegistrationHTTP(w http.ResponseWriter, r *http.Request, tail []string, convert func(error) error) error {
	service := s.options.WecomRegistration
	if r.Method == "POST" && len(tail) > 0 && tail[0] == "unbind" {
		if e := s.options.Adapters.UnbindWecom(s.ctx); e != nil {
			return convert(e)
		}
		data, e := s.options.Adapters.MaskedJSON(s.ctx)
		if e != nil {
			return convert(e)
		}
		writeJSON(w, 200, json.RawMessage(data))
		return nil
	}
	if r.Method == "POST" && len(tail) > 1 && tail[0] == "login" {
		if service == nil {
			return unsupported("adapters/wecom platform account producer")
		}
		switch tail[1] {
		case "start":
			result, e := service.StartDesktop(s.ctx)
			if e != nil {
				return convert(e)
			}
			writeJSON(w, 200, result)
			return nil
		case "poll":
			key, e := readFeishuSessionKey(r)
			if e != nil {
				return e
			}
			result, e := service.PollJSON(s.ctx, key)
			if e != nil {
				return convert(e)
			}
			if string(result.Get("connected")) == "true" {
				section := protocol.NewJSONObject(protocol.JSONField{Name: "botId", Value: result.Get("botId")}, protocol.JSONField{Name: "secret", Value: result.Get("secret")}, protocol.JSONField{Name: "pairedUsers", Value: json.RawMessage(`[]`)})
				if e := s.options.Adapters.Update(s.ctx, protocol.NewJSONObject(protocol.JSONField{Name: "wecom", Value: feishuJSON(section)})); e != nil {
					return convert(e)
				}
				data, e := s.options.Adapters.MaskedJSON(s.ctx)
				if e != nil {
					return convert(e)
				}
				writeJSON(w, 200, json.RawMessage(data))
				return nil
			}
			writeJSON(w, 200, result)
			return nil
		case "cancel":
			key, e := readFeishuSessionKey(r)
			if e != nil {
				return e
			}
			if e = service.CancelJSON(key); e != nil {
				return convert(e)
			}
			writeJSON(w, 200, map[string]string{"status": "cancelled"})
			return nil
		}
	}
	return &apiError{404, "NOT_FOUND", "Unknown WeCom adapter endpoint"}
}
