package server

import (
	"encoding/json"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// qqRegistrationHTTP is bound to the actual process-owned connector only after
// that producer is ready. Poll is synchronous in Source; success storage stays
// on the server context even if the renderer has already settled its response.
func (s *Server) qqRegistrationHTTP(w http.ResponseWriter, r *http.Request, tail []string, service *imregistration.QQService, adapters *adapterconfig.Service, convert func(error) error) error {
	if r.Method == "POST" && len(tail) > 0 && tail[0] == "unbind" {
		// Undefined identities disappear through the same raw document writer.
		if e := adapters.UnbindQQ(s.ctx); e != nil {
			return convert(e)
		}
		data, e := adapters.MaskedJSON(s.ctx)
		if e != nil {
			return convert(e)
		}
		writeJSON(w, 200, json.RawMessage(data))
		return nil
	}
	if r.Method == "POST" && len(tail) > 1 && tail[0] == "login" {
		if service == nil {
			return unsupported("adapters/qq platform account producer")
		}
		switch tail[1] {
		case "start":
			result, e := service.Start(s.ctx, protocol.NewJSONObject())
			if e != nil {
				return convert(e)
			}
			appendQQQR(&result, result.Get("verificationUrl"))
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
				section := protocol.NewJSONObject(protocol.JSONField{Name: "appId", Value: result.Get("appId")}, protocol.JSONField{Name: "appSecret", Value: result.Get("appSecret")}, protocol.JSONField{Name: "pairedUsers", Value: json.RawMessage(`[]`)}, protocol.JSONField{Name: "allowedUsers", Value: json.RawMessage(`[]`)})
				if e = adapters.Update(s.ctx, protocol.NewJSONObject(protocol.JSONField{Name: "qq", Value: feishuJSON(section)})); e != nil {
					return convert(e)
				}
				data, e := adapters.MaskedJSON(s.ctx)
				if e != nil {
					return convert(e)
				}
				writeJSON(w, 200, json.RawMessage(data))
				return nil
			}
			appendQQQR(&result, result.Get("verificationUrl"))
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
	return &apiError{404, "NOT_FOUND", "Unknown QQ adapter endpoint"}
}
func appendQQQR(result *protocol.JSONObject, raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return
	}
	if qr, e := imregistration.QRDataURL(text); e == nil {
		p, _ := json.Marshal(qr)
		result.Set("qrDataUrl", p)
	}
}
