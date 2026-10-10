package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func (s *Server) mcpHTTP(w http.ResponseWriter, r *http.Request, parts []string) (failure error) {
	defer func() {
		var err *mcpapi.Error
		if errors.As(failure, &err) {
			code := map[int]string{400: "BAD_REQUEST", 404: "NOT_FOUND", 409: "CONFLICT", 500: "INTERNAL_ERROR"}[err.Status]
			failure = &apiError{err.Status, code, err.Message}
		}
	}()
	if s.options.MCP == nil {
		return unsupported("mcp")
	}
	name, action := "", ""
	if len(parts) > 2 {
		decoded, err := url.PathUnescape(parts[2])
		if err != nil || !utf8.ValidString(decoded) {
			return &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
		}
		name = decoded
	}
	if len(parts) > 3 {
		action = parts[3]
	}
	body := protocol.NewJSONObject()
	if r.Method == "POST" || r.Method == "PUT" {
		var data json.RawMessage
		decoder := json.NewDecoder(r.Body)
		if decoder.Decode(&data) != nil {
			return badRequest("Invalid JSON body")
		}
		if err := body.UnmarshalJSON(data); err != nil {
			return badRequest("Invalid JSON body")
		}
	}
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		cwd = body.String("cwd")
	}
	service, err := s.options.MCP(r.Context(), cwd)
	if err != nil {
		return err
	}
	switch {
	case r.Method == "GET" && name == "project-paths" && action == "":
		paths, err := service.ProjectPaths(r.Context())
		if err != nil {
			return err
		}
		collator := collate.New(language.English)
		sort.SliceStable(paths, func(i, j int) bool { return collator.CompareString(paths[i], paths[j]) < 0 })
		writeJSON(w, 200, map[string]any{"projectPaths": paths})
	case r.Method == "GET" && name == "":
		servers, err := service.List(r.Context())
		if err != nil {
			return err
		}

		writeJSON(w, 200, map[string]any{"servers": servers})
	case r.Method == "GET" && name != "" && action == "status":
		server, err := service.Status(r.Context(), name)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"server": server})
	case r.Method == "POST" && name == "":
		server, err := service.Create(r.Context(), body)
		if err != nil {
			return err
		}
		writeJSON(w, 201, map[string]any{"server": server})
	case r.Method == "PUT" && name != "":
		server, err := service.Update(r.Context(), name, body)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"server": server})
	case r.Method == "DELETE" && name != "" && action == "":
		if err := service.Remove(r.Context(), name, r.URL.Query().Get("scope")); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	case r.Method == "POST" && name != "" && action == "toggle":
		server, sync, err := service.Toggle(r.Context(), name, body.String("sessionId"))
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"server": server, "sessionSync": sync})
	case r.Method == "POST" && name != "" && action == "reconnect":
		server, err := service.Reconnect(r.Context(), name)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"server": server})
	default:
		return methodNotAllowed(r.Method)
	}
	return nil
}
func (s *Server) SyncMCPToggle(ctx context.Context, sessionID, name string, config protocol.JSONObject, _ bool) (protocol.JSONObject, error) {
	encode := func(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
	response := func(applied bool, reason, message string) protocol.JSONObject {
		o := protocol.NewJSONObject(protocol.JSONField{Name: "applied", Value: encode(applied)})
		if reason != "" {
			o.Set("reason", encode(reason))
		}
		if message != "" {
			o.Set("error", encode(message))
		}
		return o
	}
	s.mu.Lock()
	runtimes := map[string]*runtimeSession{}
	for id, runtime := range s.runtimes {
		runtimes[id] = runtime
	}
	s.mu.Unlock()
	selected := response(false, "no_session", "")
	if sessionID != "" {
		selected = response(false, "not_running", "")
	}
	for id, current := range runtimes {
		current.mu.Lock()
		live, cwd, retired := current.live, current.options.WorkDir, current.retired
		current.mu.Unlock()
		if live == nil || retired {
			continue
		}
		result := response(false, "different_project", "")
		service, err := s.options.MCP(ctx, cwd)
		if err == nil && service.ProjectKey() == config.String("projectKey") {
			matches := true
			scope := config.String("scope")
			if scope == "local" || scope == "project" || scope == "user" {
				peer, failure := service.Resolve(ctx, name)
				if failure != nil {
					err = failure
				} else if peer == nil || peer.String("scope") != scope {
					matches = false
				} else {
					path, failure := service.ProjectPath(name, scope)
					err = failure
					matches = path == config.String("projectPath")
				}
			}
			if err == nil && matches {
				if control, ok := live.(ComponentSession); ok {
					timed, cancel := context.WithTimeout(ctx, 120*time.Second)
					_, err = control.ControlComponents(timed, "mcp_toggle", name)
					cancel()
				} else {
					err = errors.New("active session has no MCP component control")
				}
				if err == nil {
					result = response(true, "", "")
				}
			}
		}
		if err != nil {
			result = response(false, "failed", err.Error())
			return result, nil
		}
		if id == sessionID {
			selected = result
		}
	}
	return selected, nil
}
