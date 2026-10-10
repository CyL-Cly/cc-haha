package server

import (
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
)

func (s *Server) activityStatsHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *activitystatsapi.Service) error {
	if r.Method != "GET" {
		return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + r.Method + " not allowed"}
	}
	rangeName := "all"
	if len(parts) > 2 {
		rangeName = parts[2]
	}
	if rangeName != "all" && rangeName != "7d" && rangeName != "30d" {
		return badRequest("Unknown activity stats range: " + rangeName)
	}
	if service == nil {
		return unsupported("activity-stats")
	}
	now := service.Now()
	stats, err := service.Aggregate(r.Context(), rangeName, now)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"stats": stats, "range": rangeName, "generatedAt": now.UTC().Format("2006-01-02T15:04:05.000Z")})
	return nil
}
