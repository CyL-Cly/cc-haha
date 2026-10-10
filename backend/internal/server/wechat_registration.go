package server

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
)

// The shared dispatcher supplies its owned service when its binding is enabled.
func (s *Server) wechatRegistrationHTTP(w http.ResponseWriter, r *http.Request, tail []string, convert func(error) error, service *imregistration.WechatService) error {
	if r.Method == "POST" && len(tail) > 0 && tail[0] == "unbind" {
		if e := s.options.Adapters.UnbindWechat(s.ctx); e != nil {
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
			return unsupported("adapters/wechat platform account producer")
		}
		switch tail[1] {
		case "start":
			result, e := service.Start(s.ctx, protocol.NewJSONObject(protocol.JSONField{Name: "force", Value: json.RawMessage(`true`)}))
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
			if !result.IsObject() {
				return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
			}
			if string(result.Get("connected")) == "true" {
				base := result.Get("baseUrl")
				if len(base) == 0 || string(base) == `""` {
					base, _ = json.Marshal(imregistration.WechatDefaultBaseURL)
				}
				section := protocol.NewJSONObject(protocol.JSONField{Name: "accountId", Value: result.Get("accountId")}, protocol.JSONField{Name: "botToken", Value: result.Get("botToken")}, protocol.JSONField{Name: "baseUrl", Value: base}, protocol.JSONField{Name: "userId", Value: result.Get("userId")}, protocol.JSONField{Name: "pairedUsers", Value: json.RawMessage(`[]`)})
				if e = s.options.Adapters.UpdateWechatBinding(s.ctx, section); e != nil {
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
		}
	}
	return &apiError{404, "NOT_FOUND", "Unknown WeChat adapter endpoint"}
}
