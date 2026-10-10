package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func normalizeDeletedSessionIDs(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
		return nil, badRequest("sessionIds must be an array")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		var id string
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &id) != nil || trimJS(id) == "" {
			return nil, badRequest("sessionIds must contain only non-empty strings")
		}
		id = trimJS(id)
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, badRequest("sessionIds must include at least one session id")
	}
	return ids, nil
}
func (s *Server) markDeletedSessions(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.deleting[id] = true
		if runtime := s.runtimes[id]; runtime != nil {
			runtime.cancel()
		}
	}
}
func (s *Server) unmarkDeletedSession(id string) { s.mu.Lock(); delete(s.deleting, id); s.mu.Unlock() }

// Stop joins the actual shared runtime before unlink, so a late tool/child cannot
// recreate a deleted transcript. Failed removals reopen admission; successful
// IDs retain Source's process-lifetime deleted marker.
func (s *Server) deleteSessionData(ctx context.Context, id string) error {
	if e := s.closeSideChatsForParent(ctx, id); e != nil {
		return e
	}
	info, e := s.findSession(ctx, id)
	if e != nil {
		return e
	}
	if e = s.stopSessionAndWait(ctx, id); e != nil {
		return e
	}
	if !info.memory {
		if e = syscall.Unlink(info.Path); e != nil {
			return errors.New(strings.TrimPrefix(storage.JavaScriptErrorText(&os.PathError{Op: "unlink", Path: info.Path, Err: e}), "Error: "))
		}
		s.noteSessionMutation(info.Path)
	} else {
		s.noteSessionMutation("")
	}
	s.mu.Lock()
	delete(s.runtimes, id)
	delete(s.memoryLaunch, id)
	delete(s.knownSessions, id)
	delete(s.privateTitles, id)
	s.mu.Unlock()
	return nil
}
func (s *Server) closeDeletedSessionConnections(id string) {
	s.mu.Lock()
	sockets := []*connection{}
	for socket := range s.connections[id] {
		sockets = append(sockets, socket)
	}
	delete(s.connections, id)
	s.mu.Unlock()
	for _, socket := range sockets {
		_ = socket.ws.Close()
	}
}

// The existing IM mapping shape is preserved, including unknown fields and JS
// property order. Refresh occurs inside the shared JSON write transaction so
// independent successful deletes cannot resurrect each other's mappings.
func (s *Server) cleanupAdapterSessionMappings(id string) error {
	path := filepath.Join(s.options.ConfigDir, "adapter-sessions.json")
	return s.options.Store.WithWriteLock(path, func() error {
		data, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		var object protocol.JSONObject
		if json.Unmarshal(data, &object) != nil {
			return nil
		}
		if !object.IsObject() {
			return fmt.Errorf("adapter session mapping is not an object")
		}
		canonical, e := object.MarshalJSON()
		if e != nil {
			return e
		}
		decoder := json.NewDecoder(bytes.NewReader(canonical))
		_, _ = decoder.Token()
		removed := false
		fields := [][]byte{}
		for decoder.More() {
			start := int(decoder.InputOffset())
			_, _ = decoder.Token()
			start += bytes.IndexByte(canonical[start:], '"')
			var raw json.RawMessage
			if e := decoder.Decode(&raw); e != nil {
				return e
			}
			if string(raw) == "null" {
				return errors.New("Cannot read properties of null (reading 'sessionId')")
			}
			var entry protocol.JSONObject
			if json.Unmarshal(raw, &entry) == nil && entry.String("sessionId") == id {
				removed = true
			} else {
				fields = append(fields, canonical[start:decoder.InputOffset()])
			}
		}
		if !removed {
			return nil
		}
		next := append(append([]byte{'{'}, bytes.Join(fields, []byte{','})...), '}')
		var formatted bytes.Buffer
		if e := json.Indent(&formatted, next, "", "  "); e != nil {
			return e
		}
		formatted.WriteByte('\n')
		return s.options.Store.AtomicWriteRawJSON(path, "adapter sessions", formatted.Bytes(), storage.WriteOptions{})
	})
}

type sessionDeleteFailure struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
	Code      string `json:"code,omitempty"`
}

func (s *Server) batchDeleteSessionsHTTP(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(r.Method)
	}
	raw, e := io.ReadAll(r.Body)
	if e != nil || !json.Valid(raw) {
		return badRequest("Invalid JSON body")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)
	ids, e := normalizeDeletedSessionIDs(body["sessionIds"])
	if e != nil {
		return e
	}
	s.markDeletedSessions(ids)
	errorsByID := make([]error, len(ids))
	var wg sync.WaitGroup
	for index, id := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); errorsByID[index] = s.deleteSessionData(r.Context(), id) }()
	}
	wg.Wait()
	successes := []string{}
	failures := []sessionDeleteFailure{}
	for index, id := range ids {
		if e := errorsByID[index]; e != nil {
			failure := sessionDeleteFailure{SessionID: id, Message: e.Error()}
			var api *apiError
			if errors.As(e, &api) {
				failure.Code = api.code
			}
			failures = append(failures, failure)
			s.unmarkDeletedSession(id)
		} else {
			successes = append(successes, id)
		}
	}
	for _, id := range successes {
		s.closeDeletedSessionConnections(id)
		if e := s.cleanupAdapterSessionMappings(id); e != nil {
			return e
		}
	}
	if len(successes) > 0 {
		s.invalidateRecentProjects()
	}
	writeJSON(w, 200, map[string]any{"ok": len(failures) == 0, "successes": successes, "failures": failures})
	return nil
}
func (s *Server) deleteSessionHTTP(w http.ResponseWriter, r *http.Request, id string) error {
	s.markDeletedSessions([]string{id})
	if e := s.deleteSessionData(r.Context(), id); e != nil {
		s.unmarkDeletedSession(id)
		return e
	}
	s.closeDeletedSessionConnections(id)
	if e := s.cleanupAdapterSessionMappings(id); e != nil {
		return e
	}
	s.invalidateRecentProjects()
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
