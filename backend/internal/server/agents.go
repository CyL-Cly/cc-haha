package server

import (
	"errors"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentsapi"
)

func (s *Server) agentsHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *agentsapi.Service) error {
	if service == nil {
		return unsupported("agents")
	}
	var body []byte
	if r.Method == "POST" || r.Method == "PUT" {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return err
		}
	}
	status, response, err := service.Handle(r.Context(), r.Method, r.URL, parts, body)
	if err != nil {
		var api *agentsapi.APIError
		if errors.As(err, &api) {
			return &apiError{api.StatusCode, api.Code, api.Message}
		}
		return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}
	writeJSON(w, status, response)
	return nil
}
