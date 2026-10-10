package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func readFeishuSessionKey(r *http.Request) (json.RawMessage, error) {
	raw, e := io.ReadAll(r.Body)
	if e != nil || !json.Valid(raw) {
		return nil, badRequest("Request body must be valid JSON")
	}
	var body protocol.JSONObject
	if body.UnmarshalJSON(raw) != nil {
		return nil, badRequest("Missing or invalid sessionKey")
	}
	value := body.Get("sessionKey")
	units, e := protocol.DecodeJavaScriptJSONString(value)
	if e != nil || len(units) == 0 || len(units) > 256 {
		return nil, badRequest("Missing or invalid sessionKey")
	}
	return value, nil
}

func (s *Server) feishuRegistrationHTTP(w http.ResponseWriter, r *http.Request, tail []string, convert func(error) error) error {
	service := s.options.FeishuRegistration
	if r.Method == "POST" && len(tail) > 0 && tail[0] == "unbind" {
		if e := s.options.Adapters.UnbindFeishu(s.ctx); e != nil {
			return convert(e)
		}
		data, e := s.options.Adapters.MaskedJSON(s.ctx)
		if e != nil {
			return convert(e)
		}
		writeJSON(w, 200, json.RawMessage(data))
		return nil
	}
	if r.Method == "POST" && len(tail) > 1 && tail[0] == "registration" {
		if service == nil {
			return unsupported("adapters/feishu platform account producer")
		}
		switch tail[1] {
		case "begin":
			result, e := service.BeginDesktop(s.ctx)
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
			if result.String("status") == "success" {
				patch := protocol.NewJSONObject(protocol.JSONField{Name: "feishu", Value: feishuJSON(protocol.NewJSONObject(
					protocol.JSONField{Name: "appId", Value: result.Get("appId")}, protocol.JSONField{Name: "appSecret", Value: result.Get("appSecret")}, protocol.JSONField{Name: "domain", Value: result.Get("domain")},
					protocol.JSONField{Name: "encryptKey", Value: json.RawMessage(`""`)}, protocol.JSONField{Name: "verificationToken", Value: json.RawMessage(`""`)}, protocol.JSONField{Name: "pairedUsers", Value: json.RawMessage(`[]`)}, protocol.JSONField{Name: "allowedUsers", Value: json.RawMessage(`[]`)},
				))})
				if e := s.options.Adapters.Update(s.ctx, patch); e != nil {
					return convert(e)
				}
				data, e := s.options.Adapters.MaskedJSON(s.ctx)
				if e != nil {
					return convert(e)
				}
				writeJSON(w, 200, protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: json.RawMessage(`"success"`)}, protocol.JSONField{Name: "config", Value: data}))
				return nil
			}
			writeJSON(w, 200, result)
			return nil
		case "cancel":
			key, e := readFeishuSessionKey(r)
			if e != nil {
				return e
			}
			if e := service.CancelJSON(key); e != nil {
				return convert(e)
			}
			writeJSON(w, 200, map[string]string{"status": "cancelled"})
			return nil
		}
	}
	return &apiError{404, "NOT_FOUND", "Unknown Feishu adapter endpoint"}
}

func feishuJSON(v protocol.JSONObject) json.RawMessage { p, _ := v.MarshalJSON(); return p }
