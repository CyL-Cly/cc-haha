package server

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"io"
	"net/http"
)

func (s *Server) pluginsHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *pluginsapi.Service) error {
	if service == nil {
		return unsupported("plugins")
	}
	var body []byte
	if r.Method == "POST" {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return err
		}
	}
	status, response, err := service.Handle(r.Context(), r.Method, r.URL, parts, body)
	if err != nil {
		var api *pluginsapi.APIError
		if errors.As(err, &api) {
			return &apiError{api.StatusCode, api.Code, api.Message}
		}
		if r.Context().Err() != nil {
			w.WriteHeader(499)
			return nil
		}
		if s.options.Diagnostics != nil {
			details, _ := json.Marshal(map[string]any{"name": "Error", "message": err.Error()})
			_, _ = s.options.Diagnostics.Record(diagnosticsapi.Input{Type: "api_unhandled_error", Severity: "error", Summary: err.Error(), Details: details})
		}
		return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}
	writeJSON(w, status, response)
	return nil
}
