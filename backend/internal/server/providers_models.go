package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidermodels"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Server) providerModelsHTTP(w http.ResponseWriter, r *http.Request, service *nativeprovidermodels.Service) error {
	if service == nil {
		return unsupported("providers/models")
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || !json.Valid(raw) {
		return badRequest("Invalid JSON body")
	}
	var body protocol.JSONObject
	if json.Unmarshal(raw, &body) != nil || !body.IsObject() {
		return badRequest("Invalid input: expected object, received " + providerModelsJSONType(raw))
	}
	issues := []string{}
	for _, key := range []string{"baseUrl", "apiKey"} {
		units, err := protocol.DecodeJavaScriptJSONString(body.Get(key))
		if err != nil {
			issues = append(issues, "Invalid input: expected string, received "+providerModelsJSONType(body.Get(key)))
		} else if len(units) == 0 {
			issues = append(issues, "Too small: expected string to have >=1 characters")
		}
	}
	full := false
	if value := body.Get("isFullUrl"); value != nil {
		if string(value) != "true" && string(value) != "false" {
			issues = append(issues, "Invalid input: expected boolean, received "+providerModelsJSONType(value))
		} else {
			full = string(value) == "true"
		}
	}
	if len(issues) > 0 {
		return badRequest(strings.Join(issues, "; "))
	}
	stringField := func(key string) string {
		units, _ := protocol.DecodeJavaScriptJSONString(body.Get(key))
		return string(utf16.Decode(units))
	}
	result, err := service.Fetch(r.Context(), nativeprovidermodels.Input{BaseURL: stringField("baseUrl"), APIKey: stringField("apiKey"), IsFullURL: full})
	if err != nil {
		return err
	}
	writeJSON(w, 200, result)
	return nil
}
func providerModelsJSONType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case 'n':
		return "null"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case '{':
		return "object"
	case '[':
		return "array"
	default:
		return "number"
	}
}
