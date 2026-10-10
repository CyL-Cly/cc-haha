package server

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"io"
	"net/http"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamreader"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func (s *Server) BindTeams(api *teamsapi.Service, plans *teamplan.Service, runtime *teamworker.Runtime) error {
	if api == nil || plans == nil || runtime == nil {
		return errors.New("Teams requires complete API, approval service and actual shared session runtime")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.teamRuntimeStateLocked()
	if s.closed || s.options.Teams != nil || state.runtime != nil {
		return errors.New("Teams runtime is already bound or server closed")
	}
	s.options.Teams = api
	state.runtime = runtime
	state.plans = plans
	return nil
}
func (s *Server) BindTeamReader(o *teamreader.Options) {
	o.LocalIndex = s.options.LocalIndex
	o.HistoryBudget = s.budget
	o.FindSessionFile = func(ctx context.Context, id string) (string, error) {
		info, err := s.findSession(ctx, id)
		if err != nil {
			var api *apiError
			if errors.As(err, &api) && api.status == 404 {
				return "", nil
			}
			return "", err
		}
		return info.Path, nil
	}
	o.BirthtimeMs = func(path string, info os.FileInfo) float64 {
		value := birthtime(path, info)
		return float64(value.Unix())*1000 + float64(value.Nanosecond())/1e6
	}
}
func (s *Server) teamsHTTP(w http.ResponseWriter, r *http.Request, parts []string) error {
	s.mu.Lock()
	service := s.options.Teams
	s.mu.Unlock()
	if service == nil {
		return unsupported("teams")
	}
	var body []byte
	var err error
	if r.Method == "POST" || r.Method == "PATCH" {
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return badRequest("Invalid JSON body")
		}
	}
	response, err := service.Handle(r.Context(), r.Method, r.URL, parts, body)
	if err != nil {
		var api *teamsapi.APIError
		var plan *teamplan.Error
		var cap *teamsapi.CapabilityError
		switch {
		case errors.As(err, &api):
			return &apiError{api.Status, api.Code, api.Message}
		case errors.As(err, &plan):
			return &apiError{plan.Status, "TEAM_PLAN_CONFLICT", plan.Message}
		case errors.As(err, &cap):
			return unsupported(cap.Error())
		}
		s.options.OnUnhandled(httpapi.UnhandledError{Type: "api_unhandled_error", Severity: "error", Summary: err.Error(), Error: err})
		return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}
	writeJSON(w, 200, response)
	return nil
}
