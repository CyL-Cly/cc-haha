package server

import (
	"net/http"
	"sort"
)

type liveSessionActivity struct {
	ID            string `json:"id"`
	ActivityState string `json:"activityState"`
}

// The collection is consumed by H5 lists that have never opened a session WS.
// It must precede item lookup, and must not create a runtime to query activity.
func (s *Server) liveSessionStatusHTTP(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodGet {
		return methodNotAllowed(r.Method)
	}
	ids := map[string]bool{}
	s.mu.Lock()
	for id, runtime := range s.runtimes {
		runtime.mu.Lock()
		worker := runtime.options.TeamWorker != nil
		active := !runtime.retired && runtime.ctx.Err() == nil && (runtime.live != nil || runtime.opening != nil || hasRegisteredUserTurn(runtime))
		runtime.mu.Unlock()
		if active {
			ids[id] = worker
		}
	}
	s.mu.Unlock()
	for _, id := range s.conversations.QueuedIDs() {
		if _, exists := ids[id]; !exists {
			ids[id] = false
		}
	}
	ordered := make([]string, 0, len(ids))
	for id, worker := range ids {
		if !worker {
			ordered = append(ordered, id)
		}
	}
	sort.Strings(ordered)
	live := []liveSessionActivity{}
	for _, id := range ordered {
		status, err := s.conversations.Status(r.Context(), id)
		if err != nil {
			return err
		}
		state := status.String("activityState")
		if state == "running" || state == "waiting" {
			live = append(live, liveSessionActivity{ID: id, ActivityState: state})
		}
	}
	writeJSON(w, 200, map[string]any{"sessions": live})
	return nil
}
