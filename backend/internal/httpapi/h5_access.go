package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/h5access"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type H5AccessOptions struct {
	Service     *h5access.Service
	GraceCache  *H5GraceCache
	OnUnhandled func(UnhandledError)
}

type H5Access struct{ options H5AccessOptions }

func NewH5Access(options H5AccessOptions) (*H5Access, error) {
	if options.Service == nil || options.GraceCache == nil || options.GraceCache.service != options.Service || options.OnUnhandled == nil {
		return nil, errors.New("H5 HTTP requires the actual service, its shared grace cache, and an unhandled diagnostic callback")
	}
	return &H5Access{options: options}, nil
}

// Handle is the handleH5AccessApi seam. Local-control authentication, CORS,
// request-abort settlement and API performance spans belong to the caller.
func (handler *H5Access) Handle(writer http.ResponseWriter, request *http.Request, segments []string) {
	response, err := handler.dispatch(request, segments)
	if err != nil {
		response = handler.errorResponse(err)
	}
	response.write(writer)
}

// ServeHTTP preserves the router's WHATWG pathname split/filter. In particular
// it does not decode escapes or fix the source's raw-path authorization policy.
func (handler *H5Access) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
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

// This is the handler's /^Bearer\s+(.+)$/i, which differs from the server's
// strict process-credential parser. JS \s and dot have their own Unicode sets.
var h5BearerPattern = regexp.MustCompile(`(?i)^Bearer[\t\n\x0b\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+([^\n\r\x{2028}\x{2029}]+)$`)

func h5BearerToken(request *http.Request) string {
	match := h5BearerPattern.FindStringSubmatch(fetchHeader(request.Header, "Authorization"))
	if match == nil {
		return ""
	}
	return match[1]
}

func h5MethodNotAllowed(method, route string) error {
	return &settings.APIError{StatusCode: 405, Code: "METHOD_NOT_ALLOWED", Message: "Method " + method + " not allowed on " + route}
}

type h5ReadResult struct {
	settings    *h5access.PublicSettings
	diagnostics *h5access.Diagnostics
	err         error
}

func (handler *H5Access) read(request *http.Request) (desktopResponse, error) {
	// Promise.all starts both real reads and returns on the first failure. The
	// buffered channel lets the other operation finish without a blocked sender.
	results := make(chan h5ReadResult, 2)
	go func() {
		value, err := handler.options.Service.GetSettings(request.Context())
		results <- h5ReadResult{settings: &value, err: err}
	}()
	go func() {
		value, err := handler.options.Service.GetDiagnostics(request.Context())
		results <- h5ReadResult{diagnostics: &value, err: err}
	}()
	var value struct {
		Settings    h5access.PublicSettings `json:"settings"`
		Diagnostics h5access.Diagnostics    `json:"diagnostics"`
	}
	for count := 0; count < 2; count++ {
		result := <-results
		if result.err != nil {
			return desktopResponse{}, result.err
		}
		if result.settings != nil {
			value.Settings = *result.settings
		} else {
			value.Diagnostics = *result.diagnostics
		}
	}
	return jsonResponse(http.StatusOK, value)
}

func (handler *H5Access) settingsResponse(value h5access.PublicSettings, err error) (desktopResponse, error) {
	if err != nil {
		return desktopResponse{}, err
	}
	return jsonResponse(http.StatusOK, struct {
		Settings h5access.PublicSettings `json:"settings"`
	}{value})
}

func (handler *H5Access) dispatch(request *http.Request, segments []string) (desktopResponse, error) {
	if len(segments) <= 2 {
		switch request.Method {
		case http.MethodGet:
			return handler.read(request)
		case http.MethodPut:
			raw, err := parseJSONBody(request)
			if err != nil {
				return desktopResponse{}, err
			}
			body, valid, _ := storage.NormalizeObject(raw)
			if !valid {
				return desktopResponse{}, &settings.APIError{StatusCode: 400, Code: "BAD_REQUEST", Message: "Invalid JSON body"}
			}
			patch := storage.Object{}
			for _, key := range []string{"allowedOrigins", "publicBaseUrl", "fixedPort", "disconnectGraceSeconds"} {
				if value, present := body[key]; present {
					patch[key] = value
				}
			}
			value, err := handler.options.Service.UpdateSettings(request.Context(), patch)
			if err != nil {
				return desktopResponse{}, err
			}
			handler.options.GraceCache.Refresh(request.Context())
			return handler.settingsResponse(value, nil)
		default:
			return desktopResponse{}, h5MethodNotAllowed(request.Method, "/api/h5-access")
		}
	}
	sub := segments[2]
	if sub != "enable" && sub != "disable" && sub != "regenerate" && sub != "verify" {
		return desktopResponse{}, notFound("Unknown h5-access endpoint: " + sub)
	}
	if request.Method != http.MethodPost {
		return desktopResponse{}, h5MethodNotAllowed(request.Method, "/api/h5-access/"+sub)
	}
	switch sub {
	case "disable":
		return handler.settingsResponse(handler.options.Service.Disable(request.Context()))
	case "verify":
		valid, err := handler.options.Service.ValidateToken(request.Context(), h5BearerToken(request))
		if err != nil {
			return desktopResponse{}, err
		}
		if !valid {
			return desktopResponse{}, &settings.APIError{StatusCode: 401, Code: "UNAUTHORIZED", Message: "Invalid or missing H5 access token"}
		}
		return jsonResponse(http.StatusOK, struct {
			OK bool `json:"ok"`
		}{true})
	default:
		var result h5access.EnableResult
		var err error
		if sub == "enable" {
			result, err = handler.options.Service.Enable(request.Context())
		} else {
			result, err = handler.options.Service.RegenerateToken(request.Context())
		}
		if err != nil {
			return desktopResponse{}, err
		}
		return jsonResponse(http.StatusOK, result)
	}
}

func (handler *H5Access) errorResponse(err error) desktopResponse {
	// Managed read failures originate as ApiError.internal in the TS recoverable
	// reader. Map only that known contract; unrelated file errors stay unknown.
	var fileError *storage.FileError
	if errors.As(err, &fileError) && fileError.Operation == "read" && fileError.Label == "cc-haha managed settings" {
		err = &settings.APIError{StatusCode: 500, Code: "INTERNAL_ERROR", Message: "Failed to read " + fileError.Label + " from " + fileError.Path + ": " + storage.JavaScriptErrorText(fileError.Err)}
	}
	var api *settings.APIError
	if errors.As(err, &api) {
		code := api.Code
		if code == "" {
			code = "ERROR"
		}
		response, _ := jsonResponse(api.StatusCode, struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}{code, api.Message})
		return response
	}
	var named fetchNamedError
	if errors.As(err, &named) && named.ErrorName() == "AbortError" || err.Error() == "The operation was aborted" || err.Error() == "The connection was closed." {
		return desktopResponse{status: 499}
	}
	handler.options.OnUnhandled(UnhandledError{Type: "api_unhandled_error", Severity: "error", Summary: err.Error(), Error: err})
	response, _ := jsonResponse(http.StatusInternalServerError, struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{"INTERNAL_ERROR", "An unexpected error occurred"})
	return response
}
