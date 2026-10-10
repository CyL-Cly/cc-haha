package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Server) dingtalkRegistrationHTTP(w http.ResponseWriter, r *http.Request, action string, convert func(error) error) error {
	service := s.options.IMRegistration
	if service == nil {
		return unsupported("adapters/dingtalk registration producer")
	}
	if action == "begin" {
		// The original server races the response against request abort; it does
		// not cancel an already submitted registration operation. Server close
		// still owns the lifetime of the outbound HTTP and configuration write.
		result, err := service.Begin(s.ctx)
		if err != nil {
			return convert(err)
		}
		writeJSON(w, 200, result)
		return nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || !json.Valid(raw) {
		return badRequest("Request body must be valid JSON")
	}
	var body protocol.JSONObject
	_ = body.UnmarshalJSON(raw)
	units, err := protocol.DecodeJavaScriptJSONString(body.Get("deviceCode"))
	if err != nil || len(units) > 256 {
		return badRequest("deviceCode is required")
	}
	code := protocol.TrimJavaScriptString(body.String("deviceCode"))
	if code == "" {
		return badRequest("deviceCode is required")
	}
	result, err := service.PollJSON(s.ctx, body.Get("deviceCode"))
	if err != nil {
		// Source returns poll's promise without await in its local try. Its
		// rejection reaches the outer server boundary and has this exact body.
		if s.options.Diagnostics != nil {
			details, _ := json.Marshal(map[string]any{"path": r.URL.Path, "method": r.Method, "error": map[string]string{"name": "Error", "message": err.Error()}})
			_, _ = s.options.Diagnostics.Record(diagnosticsapi.Input{Type: "api_request_failed", Severity: "error", Summary: err.Error(), Details: details})
		}
		writeJSON(w, 500, map[string]string{"error": "Internal server error"})
		return nil
	}
	writeJSON(w, 200, result)
	return nil
}
