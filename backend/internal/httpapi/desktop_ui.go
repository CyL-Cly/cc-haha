// Package httpapi contains handler seams for the local server. Server routing,
// CORS, authentication middleware and process startup remain the caller's job.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/uipreferences"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// UnhandledError carries the legacy errorResponse diagnostic event. The
// callback must record the event and console evidence in the Go diagnostics
// boundary; this package does not fall back to the TS diagnostics service.
type UnhandledError struct {
	Type, Severity, Summary string
	Error                   error
}

type DesktopUIOptions struct {
	Service       *uipreferences.Service
	Authorization func() access.Authorization
	OnUnhandled   func(UnhandledError)
}

type DesktopUI struct{ options DesktopUIOptions }

func NewDesktopUI(options DesktopUIOptions) (*DesktopUI, error) {
	if options.Service == nil || options.Authorization == nil || options.OnUnhandled == nil {
		return nil, errors.New("desktop UI HTTP requires the actual preferences service, explicit credentials, and an unhandled diagnostic callback")
	}
	return &DesktopUI{options}, nil
}

// Handle receives the router's explicit pathname segments, like the source
// handleDesktopUiApi. The request URL has no other effect on this handler.
func (handler *DesktopUI) Handle(writer http.ResponseWriter, request *http.Request, segments []string) {
	response, err := handler.dispatch(request, segments)
	if err != nil {
		response = handler.errorResponse(err)
	}
	response.write(writer)
}

// ServeHTTP is a convenience seam, not the server's authorization middleware.
// The legacy router splits WHATWG URL.pathname without decoding escapes or
// cleaning repeated slashes. The already pinned parser retains those rules.
func (handler *DesktopUI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	href := request.URL.String()
	if !request.URL.IsAbs() {
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		href = scheme + "://" + request.Host + request.URL.RequestURI()
	}
	url, err := weburl.Parse(href)
	if err != nil {
		handler.errorResponse(err).write(writer)
		return
	}
	segments := []string{}
	for _, segment := range strings.Split(url.Pathname(), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	handler.Handle(writer, request, segments)
}

func segment(segments []string, index int) string {
	if index >= len(segments) {
		return "undefined"
	}
	return segments[index]
}

func methodNotAllowed(method string) error {
	return &settings.APIError{StatusCode: 405, Code: "METHOD_NOT_ALLOWED", Message: "Method " + method + " not allowed"}
}
func notFound(message string) error {
	return &settings.APIError{StatusCode: 404, Code: "NOT_FOUND", Message: message}
}

func parseJSONBody(request *http.Request) (json.RawMessage, error) {
	data, err := readBody(request)
	if err == nil {
		data = storage.DecodeUTF8(data)
		// Fetch's UTF-8 decoder removes a leading BOM, but not one after spaces.
		data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	}
	if err != nil || !json.Valid(data) {
		return nil, &settings.APIError{StatusCode: 400, Code: "BAD_REQUEST", Message: "Invalid JSON body"}
	}
	// Keep parsed numeric lexemes until the actual business checks. A JSON
	// stringify here would turn Infinity into null before normalization.
	return json.RawMessage(data), nil
}

func readBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return []byte{}, nil
	}
	return io.ReadAll(request.Body)
}

// Fetch Headers.get combines duplicate values with comma-space. net/http's
// Header.Get returns only the first field, which could widen pet authorization
// or accept a repeated avatar Content-Type that the source rejects.
func fetchHeader(headers http.Header, name string) string {
	return strings.Join(headers.Values(name), ", ")
}

func petPreferences(preferences uipreferences.Preferences) json.RawMessage {
	// Only the actual service constructs this normalized, valid JSON object.
	// Keep its raw pet values until the response's JS serialization boundary.
	var fields struct {
		Pet json.RawMessage `json:"pet"`
	}
	_ = json.Unmarshal(json.RawMessage(preferences), &fields)
	return fields.Pet
}

func preferencesResponse(preferences uipreferences.Preferences, err error) (desktopResponse, error) {
	if err != nil {
		return desktopResponse{}, err
	}
	return jsonResponse(http.StatusOK, struct {
		OK          bool                      `json:"ok"`
		Preferences uipreferences.Preferences `json:"preferences"`
	}{true, preferences})
}

func (handler *DesktopUI) dispatch(request *http.Request, segments []string) (desktopResponse, error) {
	if sub := segment(segments, 2); sub != "preferences" {
		return desktopResponse{}, notFound("Unknown desktop UI endpoint: " + sub)
	}
	if len(segments) <= 3 {
		if request.Method != http.MethodGet {
			return desktopResponse{}, methodNotAllowed(request.Method)
		}
		result, err := handler.options.Service.ReadPreferences(request.Context())
		if err != nil {
			return desktopResponse{}, err
		}
		return jsonResponse(http.StatusOK, result)
	}
	switch detail := segments[3]; detail {
	case "sidebar", "project-display-name", "profile":
		if detail == "profile" && len(segments) > 4 {
			if segments[4] != "avatar" {
				return desktopResponse{}, notFound("Unknown desktop UI profile endpoint: " + segments[4])
			}
			return handler.avatar(request)
		}
		if request.Method != http.MethodPut {
			return desktopResponse{}, methodNotAllowed(request.Method)
		}
		body, err := parseJSONBody(request)
		if err != nil {
			return desktopResponse{}, err
		}
		switch detail {
		case "sidebar":
			return preferencesResponse(handler.options.Service.UpdateSidebarPreferences(request.Context(), body))
		case "profile":
			return preferencesResponse(handler.options.Service.UpdateProfilePreferences(request.Context(), body))
		default:
			update, err := handler.options.Service.UpdateProjectDisplayName(request.Context(), body)
			if err != nil {
				return desktopResponse{}, err
			}
			var fields struct {
				ProjectKey  json.RawMessage `json:"projectKey"`
				DisplayName json.RawMessage `json:"displayName"`
			}
			// The service constructs this exact two-field JSON result.
			_ = json.Unmarshal(update, &fields)
			return jsonResponse(http.StatusOK, struct {
				OK          bool            `json:"ok"`
				ProjectKey  json.RawMessage `json:"projectKey"`
				DisplayName json.RawMessage `json:"displayName"`
			}{true, fields.ProjectKey, fields.DisplayName})
		}
	case "pet":
		if request.Method == http.MethodGet {
			result, err := handler.options.Service.ReadPreferences(request.Context())
			if err != nil {
				return desktopResponse{}, err
			}
			return jsonResponse(http.StatusOK, struct {
				Exists bool            `json:"exists"`
				Pet    json.RawMessage `json:"pet"`
			}{result.Exists, petPreferences(result.Preferences)})
		}
		if request.Method != http.MethodPut {
			return desktopResponse{}, methodNotAllowed(request.Method)
		}
		body, err := parseJSONBody(request)
		if err != nil {
			return desktopResponse{}, err
		}
		preferences, err := handler.options.Service.UpdatePetPreferences(request.Context(), body)
		if err != nil {
			return desktopResponse{}, err
		}
		credentials := http.Header{"Authorization": {fetchHeader(request.Header, "Authorization")}}
		if handler.options.Authorization().PetAuthorized(credentials, nil) {
			return jsonResponse(http.StatusOK, struct {
				OK  bool            `json:"ok"`
				Pet json.RawMessage `json:"pet"`
			}{true, petPreferences(preferences)})
		}
		return preferencesResponse(preferences, nil)
	default:
		return desktopResponse{}, notFound("Unknown desktop UI preferences endpoint: " + detail)
	}
}

func (handler *DesktopUI) avatar(request *http.Request) (desktopResponse, error) {
	switch request.Method {
	case http.MethodGet:
		avatar, err := handler.options.Service.ReadProfileAvatar(request.Context())
		if err != nil {
			return desktopResponse{}, err
		}
		if avatar == nil {
			return desktopResponse{}, notFound("Profile avatar is not configured")
		}
		return desktopResponse{status: http.StatusOK, headers: http.Header{"Content-Type": {avatar.ContentType}, "Cache-Control": {"no-store"}}, body: avatar.Bytes}, nil
	case http.MethodPut:
		data, err := readBody(request)
		if err != nil {
			return desktopResponse{}, err
		}
		return preferencesResponse(handler.options.Service.UpdateProfileAvatar(request.Context(), data, fetchHeader(request.Header, "Content-Type")))
	case http.MethodDelete:
		return preferencesResponse(handler.options.Service.ClearProfileAvatar(request.Context()))
	default:
		return desktopResponse{}, methodNotAllowed(request.Method)
	}
}
