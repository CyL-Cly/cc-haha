package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

type desktopResponse struct {
	status  int
	headers http.Header
	body    []byte
}

func jsonResponse(status int, value any) (desktopResponse, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return desktopResponse{}, err
	}
	data, err := protocol.ParseAndStringifyJavaScriptJSON(buffer.Bytes())
	return desktopResponse{status: status, headers: http.Header{"Content-Type": {"application/json;charset=utf-8"}}, body: data}, err
}

func (response desktopResponse) write(writer http.ResponseWriter) {
	for name, values := range response.headers {
		writer.Header()[name] = append([]string{}, values...)
	}
	writer.WriteHeader(response.status)
	if len(response.body) > 0 {
		_, _ = writer.Write(response.body)
	}
}

// Named errors are supplied by request-body bridges when preserving Fetch's
// AbortError name. Go stack traces are not presented as a JS-equivalent stack.
type fetchNamedError interface {
	error
	ErrorName() string
}

func (handler *DesktopUI) errorResponse(err error) desktopResponse {
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
