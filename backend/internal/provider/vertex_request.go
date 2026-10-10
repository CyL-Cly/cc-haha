package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// VertexConfig follows the separately reviewed official Vertex SDK 0.14.4
// wrapper. The credential callback is supplied by the same Go SDK owner; it is
// not an independent model loop or a TypeScript business fallback.
type VertexConfig struct {
	Region, ProjectID string
	Headers           func(context.Context) (http.Header, string, error)
}

func (config *VertexConfig) adapt(ctx context.Context, request *http.Request, prepared *MessagesRequest) error {
	project := config.ProjectID
	if config.Headers != nil {
		headers, clientProject, err := config.Headers(ctx)
		if err != nil {
			return err
		}
		if project == "" {
			// Installed GoogleAuth10 returns WHATWG Headers; the reviewed
			// wrapper indexes authHeaders["x-goog-user-project"], which is
			// undefined on that class. Do not turn quota into project discovery.
			project = clientProject
		}
		for key, values := range headers {
			request.Header.Del(key)
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
		for key, values := range prepared.Headers {
			request.Header.Del(key)
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
	}
	var body protocol.JSONObject
	if err := body.UnmarshalJSON(prepared.Body); err != nil {
		return errors.New("Expected request body to be an object for post /v1/messages")
	}
	if !jsonTruthy(body.Get("anthropic_version")) {
		body.Set("anthropic_version", json.RawMessage(`"vertex-2023-10-16"`))
	}
	path := prepared.Path
	if path == "/v1/messages" || path == "/v1/messages?beta=true" {
		if project == "" {
			return errors.New("No projectId was given and it could not be resolved from credentials. The client should be instantiated with the `projectId` option or the `ANTHROPIC_VERTEX_PROJECT_ID` environment variable should be set.")
		}
		modelJSON, err := protocol.JavaScriptStringJSON(body.Get("model"))
		if err != nil {
			return err
		}
		var model string
		if err = json.Unmarshal(modelJSON, &model); err != nil {
			return err
		}
		body.Delete("model")
		specifier := "rawPredict"
		if jsonTruthy(body.Get("stream")) {
			specifier = "streamRawPredict"
		}
		path = "/projects/" + project + "/locations/" + config.Region + "/publishers/anthropic/models/" + model + ":" + specifier
	} else if path == "/v1/messages/count_tokens" || path == "/v1/messages/count_tokens?beta=true" {
		if project == "" {
			return errors.New("No projectId was given and it could not be resolved from credentials. The client should be instantiated with the `projectId` option or the `ANTHROPIC_VERTEX_PROJECT_ID` environment variable should be set.")
		}
		path = "/projects/" + project + "/locations/" + config.Region + "/publishers/anthropic/models/count-tokens:rawPredict"
	}
	if path != prepared.Path {
		base := strings.TrimSuffix(request.URL.String(), prepared.Path)
		target, err := url.Parse(strings.TrimSuffix(base, "/") + path)
		if err != nil {
			return err
		}
		request.URL = target
	}
	raw, err := body.MarshalJSON()
	if err != nil {
		return err
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return err
	}
	request.Body = ioBody(raw)
	request.ContentLength = int64(len(raw))
	request.GetBody = func() (io.ReadCloser, error) { return ioBody(raw), nil }
	return nil
}
