package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var configurationKey = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

// HandleHTTP preserves the original asynchronous 202 action contract. A
// successful admission means the lifecycle started, never that setup is ready.
func (s *Service) HandleHTTP(w http.ResponseWriter, r *http.Request, parts []string) {
	HandleHTTP(w, r, parts, func(context.Context) (*Service, error) { return s, nil })
}

// Service resolution follows validation so invalid requests cannot initialize
// credential or installation state.
func HandleHTTP(w http.ResponseWriter, r *http.Request, parts []string, get func(context.Context) (*Service, error)) {
	status, value, err := httpRequest(r, parts, get)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			w.WriteHeader(499)
			return
		}
		status, code, message := 500, "INTERNAL_ERROR", "An unexpected error occurred"
		var own *Error
		if errors.As(err, &own) {
			status, message = own.Status, own.Message
			switch status {
			case 404:
				code = "NOT_FOUND"
			case 409:
				code = "CONFLICT"
			case 405:
				code = "METHOD_NOT_ALLOWED"
			default:
				code = "BAD_REQUEST"
			}
		}
		writeHTTP(w, status, map[string]string{"error": code, "message": message})
		return
	}
	writeHTTP(w, status, value)
}
func writeHTTP(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
func httpRequest(r *http.Request, parts []string, get func(context.Context) (*Service, error)) (int, any, error) {
	id, action := "", ""
	if len(parts) > 2 {
		id = parts[2]
	}
	if len(parts) > 3 {
		action = parts[3]
	}
	actions := []string{"prepare", "authenticate", "check", "cancel", "deactivate", "remove"}
	if len(parts) > 4 || action != "" && !slices.Contains(actions, action) {
		return 0, nil, apiError(404, "Unknown connector endpoint")
	}
	if r.Method == http.MethodGet && action == "" {
		s, err := get(r.Context())
		if err != nil {
			return 0, nil, err
		}
		if id != "" {
			value, err := s.Get(id)
			return 200, map[string]any{"connector": value}, err
		}
		value, err := s.List()
		return 200, map[string]any{"items": value}, err
	}
	if r.Method != http.MethodPost || id == "" || action == "" {
		return 0, nil, apiError(405, "Method not allowed")
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return 0, nil, apiError(400, "Expected a JSON object")
	}
	var body protocol.JSONObject
	if body.UnmarshalJSON(data) != nil || !body.IsObject() {
		return 0, nil, apiError(400, "Expected a JSON object")
	}
	for _, p := range body.OwnProperties() {
		var key string
		_ = json.Unmarshal(p.NameJSON, &key)
		if !slices.Contains([]string{"acknowledgeSharedCredentials", "sessionId", "configuration"}, key) {
			return 0, nil, apiError(400, "Unknown connector action field")
		}
	}
	options := ActionOptions{}
	if value := body.Get("acknowledgeSharedCredentials"); value != nil {
		value = bytes.TrimSpace(value)
		if string(value) != "true" && string(value) != "false" {
			return 0, nil, apiError(400, "acknowledgeSharedCredentials must be a boolean")
		}
		options.AcknowledgeSharedCredentials = string(value) == "true"
	}
	if value := body.Get("sessionId"); value != nil {
		units, err := protocol.DecodeJavaScriptJSONString(value)
		if err != nil || len(units) > 256 || trimJS(string(utf16.Decode(units))) == "" {
			return 0, nil, apiError(400, "Invalid sessionId")
		}
		options.SessionID = string(utf16.Decode(units))
	}
	if value := body.Get("configuration"); value != nil {
		if action != "prepare" && action != "authenticate" {
			return 0, nil, apiError(400, "Configuration is only accepted during setup or authentication")
		}
		var config protocol.JSONObject
		if config.UnmarshalJSON(value) != nil || !config.IsObject() || len(config.OwnProperties()) > 8 {
			return 0, nil, apiError(400, "Invalid connector configuration")
		}
		options.Configuration = map[string]string{}
		for _, p := range config.OwnProperties() {
			var key string
			_ = json.Unmarshal(p.NameJSON, &key)
			units, err := protocol.DecodeJavaScriptJSONString(p.Value)
			text := string(utf16.Decode(units))
			if !configurationKey.MatchString(key) || err != nil || len(units) > 8192 || strings.ContainsAny(text, "\r\n\x00") {
				return 0, nil, apiError(400, "Invalid connector configuration")
			}
			options.Configuration[key] = text
		}
	}
	s, err := get(r.Context())
	if err != nil {
		return 0, nil, err
	}
	value, err := s.Action(id, action, options)
	return 202, map[string]any{"connector": value}, err
}
