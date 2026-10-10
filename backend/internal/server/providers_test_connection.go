package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidertest"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The unsaved route has its own validation. The saved route never accepts a
// destination/protocol/auth override alongside a credential loaded from disk.
func (s *Server) providerConnectionTestHTTP(w http.ResponseWriter, r *http.Request, id string, service *nativeprovidertest.Service) error {
	if service == nil {
		return unsupported("providers/test")
	}
	if r.Method != "POST" {
		return methodNotAllowed(r.Method)
	}
	raw, readErr := io.ReadAll(r.Body)
	var result nativeprovidertest.Result
	var failure error
	details := map[string]any{"providerId": nil}
	if id == "" {
		if readErr != nil || !json.Valid(raw) {
			return badRequest("Invalid JSON body")
		}
		input, issues := nativeprovidertest.ParseInput(raw)
		if len(issues) > 0 {
			return badRequest(strings.Join(issues, "; "))
		}
		details["baseUrl"], details["apiFormat"], details["modelId"] = input.BaseURL, input.APIFormat, input.ModelID
		result, failure = service.Test(r.Context(), input)
	} else {
		details["providerId"] = id
		body := protocol.NewJSONObject()
		if json.Valid(raw) {
			_ = json.Unmarshal(raw, &body)
		}
		if body.IsObject() {
			for _, key := range []string{"baseUrl", "apiFormat", "authStrategy"} {
				if body.Get(key) != nil {
					return badRequest(key + " cannot be overridden when testing with a saved provider key")
				}
			}
		}
		model := ""
		if v := body.Get("modelId"); v != nil {
			if _, err := protocol.DecodeJavaScriptJSONString(v); err != nil {
				return badRequest("modelId must be a string")
			}
			model = body.String("modelId")
			details["modelId"] = model
		}
		result, failure = service.Saved(r.Context(), id, model)
	}
	if failure != nil {
		if id == "" && s.options.Diagnostics != nil {
			detail, _ := json.Marshal(map[string]any{"providerId": nil, "error": map[string]string{"name": "Error", "message": failure.Error()}})
			_, _ = s.options.Diagnostics.Record(diagnosticsapi.Input{Type: "provider_test_failed", Severity: "warn", Summary: failure.Error(), Details: detail})
		}
		return failure
	}
	if !result.Connectivity.Success || result.Proxy != nil && !result.Proxy.Success {
		summary := result.Connectivity.Error
		if summary == "" && result.Proxy != nil {
			summary = result.Proxy.Error
		}
		if summary == "" {
			summary = "Provider test failed"
		}
		details["connectivity"] = result.Connectivity
		if result.Proxy != nil {
			details["proxy"] = result.Proxy
		}
		if id != "" {
			status := result.Connectivity.HTTPStatus
			if status == nil && result.Proxy != nil {
				status = result.Proxy.HTTPStatus
			}
			if status != nil {
				details["httpStatus"] = *status
			}
		}
		if s.options.Diagnostics != nil {
			detail, _ := json.Marshal(details)
			_, _ = s.options.Diagnostics.Record(diagnosticsapi.Input{Type: "provider_test_failed", Severity: "warn", Summary: summary, Details: detail})
		}
	}
	writeJSON(w, 200, map[string]any{"result": result})
	return nil
}
