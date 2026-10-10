package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/searchapi"
)

func (s *Server) searchHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *searchapi.Service) error {
	if service == nil {
		return unsupported("search")
	}
	var body []byte
	if r.Method == "POST" {
		var e error
		body, e = io.ReadAll(r.Body)
		if e != nil {
			if requestSignalContext(r).Err() != nil {
				w.WriteHeader(499)
				return nil
			}
			return e
		}
	}
	sub := ""
	if len(parts) > 2 {
		sub = parts[2]
	}
	ctx := r.Context()
	if sub == "sessions" {
		ctx = requestSignalContext(r)
	}
	status, response, err := service.Handle(ctx, r.Method, sub, body)
	if err != nil {
		if requestSignalContext(r).Err() != nil {
			w.WriteHeader(499)
			return nil
		}
		var api *searchapi.APIError
		if errors.As(err, &api) {
			return &apiError{api.Status, api.Code, api.Message}
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
