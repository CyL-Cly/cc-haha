package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
)

var diagnosticsLimitInteger = regexp.MustCompile(`^[+-]?\d+`)

func (s *Server) diagnosticsHTTP(w http.ResponseWriter, r *http.Request, parts []string, service *diagnosticsapi.Service) (failure error) {
	defer func() {
		if failure == nil {
			return
		}
		var api *apiError
		if errors.As(failure, &api) {
			return
		}
		if service != nil {
			details, _ := json.Marshal(map[string]string{"name": "Error", "message": failure.Error()})
			_, _ = service.Record(diagnosticsapi.Input{Type: "api_unhandled_error", Severity: "error", Summary: failure.Error(), Details: details})
		}
		failure = &apiError{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}()
	if service == nil {
		return unsupported("diagnostics")
	}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	switch {
	case action == "" && r.Method == "DELETE":
		if err := service.Clear(); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	case action == "status" && r.Method == "GET":
		status, err := service.GetStatus()
		if err != nil {
			return err
		}
		writeJSON(w, 200, status)
	case action == "events" && r.Method == "GET":
		value := r.URL.Query().Get("limit")
		limit := 100
		if match := diagnosticsLimitInteger.FindString(strings.TrimSpace(value)); match != "" {
			if parsed, err := strconv.ParseFloat(match, 64); err == nil {
				if parsed > 1000 {
					limit = 1000
				} else if parsed < 1 {
					limit = 1
				} else {
					limit = int(parsed)
				}
			}
		}
		events, err := service.ReadRecent(limit)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"events": events})
	case action == "events" && r.Method == "POST":
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		if !json.Valid(raw) {
			return badRequest("Invalid JSON body")
		}
		var body map[string]json.RawMessage
		_ = json.Unmarshal(raw, &body)
		read := func(name string) string { var value string; _ = json.Unmarshal(body[name], &value); return value }
		typ := strings.TrimSpace(read("type"))
		if typ == "" {
			typ = "client_diagnostic_event"
		}
		units := utf16.Encode([]rune(typ))
		if len(units) > 128 {
			typ = string(utf16.Decode(units[:128]))
		}
		severity := read("severity")
		if !diagnosticsapi.IsSeverity(severity) {
			severity = "info"
		}
		summary := read("summary")
		if strings.TrimSpace(summary) == "" {
			summary = typ
		}
		event, err := service.Record(diagnosticsapi.Input{Type: typ, Severity: severity, Summary: summary, SessionID: read("sessionId"), Details: body["details"]})
		if err != nil {
			return &apiError{500, "INTERNAL_ERROR", err.Error()}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "eventId": event.ID})
	case action == "issue-report" && r.Method == "GET":
		report, err := service.BuildIssueReport()
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"report": report})
	case action == "export" && r.Method == "POST":
		bundle, err := service.ExportBundle()
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"bundle": bundle})
	case action == "open-log-dir" && r.Method == "POST":
		if err := service.OpenLogDir(r.Context()); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	case action == "local-index" && len(parts) == 3 && r.Method == "GET":
		status, err := service.LocalIndexStatus()
		if err != nil {
			var unsupportedError *diagnosticsapi.CapabilityError
			if errors.As(err, &unsupportedError) {
				return unsupported(unsupportedError.Capability)
			}
			return err
		}
		writeJSON(w, 200, status)
	case action == "local-index" && len(parts) == 4 && parts[3] == "rebuild" && r.Method == "POST":
		status, err := service.RebuildLocalIndex(r.Context())
		if err != nil {
			var unsupportedError *diagnosticsapi.CapabilityError
			if errors.As(err, &unsupportedError) {
				return unsupported(unsupportedError.Capability)
			}
			return err
		}
		writeJSON(w, 200, status)
	default:
		if action == "" {
			action = "(root)"
		}
		return notFound("Unknown diagnostics endpoint: " + action)
	}
	return nil
}
