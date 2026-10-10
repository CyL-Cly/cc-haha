package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/doctorapi"
)

func (s *Server) doctorHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *doctorapi.Service) error {
	if service == nil {
		return unsupported("doctor")
	}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	if r.Method == "GET" && (action == "" || action == "report") {
		report, err := service.ForProject(r.URL.Query().Get("cwd")).GetReport(r.Context())
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"report": report})
		return nil
	}
	if r.Method == "POST" && action == "repair" {
		body := map[string]json.RawMessage{}
		if r.Header.Get("Content-Length") != "" || r.Header.Get("Transfer-Encoding") != "" || r.Header.Get("Content-Type") != "" || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				return err
			}
			if !json.Valid(raw) {
				return badRequest("Invalid JSON body")
			}
			_ = json.Unmarshal(raw, &body)
		}
		cwd := r.URL.Query().Get("cwd")
		var bodyCwd string
		if raw := body["cwd"]; len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &bodyCwd) == nil {
			cwd = bodyCwd
		}
		ids := []string{}
		var values []json.RawMessage
		_ = json.Unmarshal(body["targetIds"], &values)
		for _, raw := range values {
			var id string
			if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &id) == nil && id != "" {
				ids = append(ids, id)
			}
		}
		result, err := service.ForProject(cwd).Repair(r.Context(), ids)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"result": result})
		return nil
	}
	if action == "" {
		return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + r.Method + " not allowed on /api/doctor"}
	}
	return notFound("Unknown doctor endpoint: " + action)
}
