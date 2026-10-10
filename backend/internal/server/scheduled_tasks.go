package server

import (
	"errors"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/scheduler"
)

func (s *Server) scheduledTasksHTTP(w http.ResponseWriter, r *http.Request, parts []string) error {
	service := s.options.Scheduler
	if service == nil {
		return unsupported("scheduled-tasks")
	}
	var body []byte
	if r.Method == "POST" || r.Method == "PUT" {
		var e error
		body, e = io.ReadAll(r.Body)
		if e != nil {
			return e
		}
	}
	status, response, e := service.Handle(r.Context(), r.Method, r.URL, parts, body)
	if e != nil {
		var own *scheduler.Error
		var capability *scheduler.CapabilityError
		switch {
		case errors.As(e, &own):
			return &apiError{own.Status, own.Code, own.Message}
		case errors.As(e, &capability):
			return &apiError{501, "GO_MIGRATION_NOT_READY", capability.Error()}
		default:
			return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
		}
	}
	writeJSON(w, status, response)
	return nil
}
