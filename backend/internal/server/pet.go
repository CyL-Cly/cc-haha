package server

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/petaccess"
	"net/http"
)

type petAccessKey struct{}

func isPetRequest(r *http.Request) bool { v, _ := r.Context().Value(petAccessKey{}).(bool); return v }
func (s *Server) petSessions(ctx context.Context, limit float64) ([]sessionInfo, error) {
	offset := 0.0
	if rows, _, ok := s.indexedSessionList(ctx, localindex.SessionListOptions{Limit: &limit, Offset: &offset}); ok {
		return rows, nil
	}
	rows, e := s.allSessionsFromFiles(ctx)
	if e != nil {
		return nil, e
	}
	return rows[:int(min(float64(len(rows)), limit))], nil
}
func (s *Server) petSessionAllowed(ctx context.Context, id string) (bool, error) {
	rows, e := s.petSessions(ctx, petaccess.SessionLimit)
	if e != nil {
		return false, e
	}
	for _, row := range rows {
		if row.ID == id {
			return true, nil
		}
	}
	return false, nil
}
func (s *Server) petSessionListHTTP(w http.ResponseWriter, r *http.Request, limit float64) error {
	rows, e := s.petSessions(r.Context(), min(limit, petaccess.SessionLimit))
	if e != nil {
		return e
	}
	projection := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		projection = append(projection, map[string]any{"id": row.ID, "title": row.Title, "createdAt": row.CreatedAt, "modifiedAt": row.ModifiedAt, "messageCount": row.MessageCount, "projectPath": "", "workDir": nil, "workDirExists": false})
	}
	writeJSON(w, 200, map[string]any{"sessions": projection, "total": len(rows)})
	return nil
}
