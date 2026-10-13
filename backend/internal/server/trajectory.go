package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/trajectory"
)

func (s *Server) trajectoryHTTP(w http.ResponseWriter, r *http.Request, parts []string) (err error) {
	defer func() {
		if err != nil {
			var e *sessionstore.APIError
			if errors.As(err, &e) {
				err = &apiError{e.StatusCode, e.Code, e.Message}
			} else if errors.Is(err, context.Canceled) {
				w.WriteHeader(499)
				err = nil
			}
		}
	}()
	if r.Method != http.MethodGet {
		return methodNotAllowed(r.Method)
	}
	info, e := s.findSession(r.Context(), parts[2])
	if e != nil {
		return e
	}
	agent, e := trajectory.AgentID(r.URL.Query().Get("agentId"))
	if e != nil {
		return e
	}
	directory := filepath.Join(filepath.Dir(info.Path), info.ID)
	source := trajectory.Source{Path: info.Path, SnapshotPath: filepath.Join(directory, "prompt-snapshots.jsonl"), Scope: "main", Main: true}
	if agent != "" {
		source.Path = filepath.Join(directory, "subagents", "agent-"+agent+".jsonl")
		source.Scope = agent
		source.Main = false
	}
	if len(parts) == 4 {
		page, e := s.trajectory.Page(r.Context(), source, trajectory.PageOptions{Cursor: r.URL.Query().Get("cursor"), After: r.URL.Query().Get("after")})
		if e != nil {
			return e
		}
		writeJSON(w, 200, page)
		return nil
	}
	if len(parts) != 6 {
		return notFound("Trajectory route not found")
	}
	leaf, e := url.PathUnescape(parts[5])
	if e != nil {
		return badRequest("Invalid trajectory path")
	}
	switch parts[4] {
	case "rows":
		detail, e := s.trajectory.Detail(r.Context(), source, leaf, r.URL.Query().Get("loc"))
		if e != nil {
			return e
		}
		writeJSON(w, 200, detail)
		return nil
	case "snapshots":
		blob, e := s.trajectory.Snapshot(r.Context(), source.SnapshotPath, leaf)
		if e != nil {
			return e
		}
		writeJSON(w, 200, blob)
		return nil
	default:
		return notFound("Trajectory route not found")
	}
}
