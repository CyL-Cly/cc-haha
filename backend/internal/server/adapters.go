package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
)

func (s *Server) adaptersHTTP(w http.ResponseWriter, r *http.Request, parts []string) error {
	service := s.options.Adapters
	if service == nil {
		return unsupported("adapters")
	}
	convert := func(err error) error {
		if err == nil {
			return nil
		}
		if api, ok := err.(*adapterconfig.Error); ok {
			return &apiError{api.StatusCode, api.Code, api.Message}
		}
		if api, ok := err.(*imregistration.Error); ok {
			return &apiError{api.Status, api.Code, api.Message}
		}
		return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}
	if len(parts) > 2 {
		switch parts[2] {
		case "slack":
			if len(parts) > 3 && parts[3] == "manifest" && r.Method == "GET" {
				writeJSON(w, 200, map[string]string{"manifest": imruntime.SlackManifestJSON(), "createAppUrl": imruntime.SlackCreateAppURL()})
				return nil
			}
			if len(parts) > 3 && parts[3] == "unbind" && r.Method == "POST" {
				if err := service.UnbindSlack(r.Context()); err != nil {
					return convert(err)
				}
				data, err := service.MaskedJSON(r.Context())
				if err != nil {
					return convert(err)
				}
				writeJSON(w, 200, json.RawMessage(data))
				return nil
			}
			return &apiError{404, "NOT_FOUND", "Unknown Slack adapter endpoint"}
		case "feishu":
			return s.feishuRegistrationHTTP(w, r, parts[3:], convert)
		case "wecom":
			return s.wecomRegistrationHTTP(w, r, parts[3:], convert)
		case "qq":
			return s.qqRegistrationHTTP(w, r, parts[3:], s.options.QQRegistration, service, convert)
		case "wechat":
			return s.wechatRegistrationHTTP(w, r, parts[3:], convert, s.options.WechatRegistration)
		case "whatsapp":
			return s.whatsappRegistrationHTTP(w, r, parts[3:], convert)
		case "dingtalk":
			if len(parts) > 3 && parts[3] == "unbind" && r.Method == "POST" {
				if err := service.UnbindDingTalk(r.Context()); err != nil {
					return convert(err)
				}
				data, err := service.MaskedJSON(r.Context())
				if err != nil {
					return convert(err)
				}
				writeJSON(w, 200, json.RawMessage(data))
				return nil
			}
			if len(parts) > 4 && parts[3] == "registration" && (parts[4] == "begin" || parts[4] == "poll") && r.Method == "POST" {
				return s.dingtalkRegistrationHTTP(w, r, parts[4], convert)
			}
		}
	}

	switch r.Method {
	case "GET":
		data, err := service.MaskedJSON(r.Context())
		if err != nil {
			return convert(err)
		}
		writeJSON(w, 200, json.RawMessage(data))
		return nil
	case "PUT":
		data, err := io.ReadAll(r.Body)
		if err != nil || !json.Valid(data) {
			return badRequest("Request body must be valid JSON")
		}
		patch, err := adapterconfig.ParsePatch(data)
		if err != nil {
			return convert(err)
		}
		if err = service.Update(r.Context(), patch); err != nil {
			return convert(err)
		}
		masked, err := service.MaskedJSON(r.Context())
		if err != nil {
			return convert(err)
		}
		writeJSON(w, 200, json.RawMessage(masked))
		return nil
	default:
		return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + r.Method + " not allowed"}
	}
}
