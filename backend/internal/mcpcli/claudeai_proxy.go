package mcpcli

import (
	"context"
	"errors"
	"net/http"
)

// ClaudeAIProxyTransport is the original proxy fetch wrapper around the actual
// MCP HTTP transport. The caller supplies the existing credential/refresh owner.
type ClaudeAIProxyTransport struct {
	Inner           http.RoundTripper
	CheckAndRefresh func(context.Context) error
	Token           func(context.Context) (*string, error)
	Handle401       func(context.Context, string) (bool, error)
}

func (t *ClaudeAIProxyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.Inner == nil || t.CheckAndRefresh == nil || t.Token == nil || t.Handle401 == nil {
		return nil, errors.New("Claude.ai proxy requires actual transport and shared credential refresh bindings")
	}
	doRequest := func(retry bool) (*http.Response, string, error) {
		if e := t.CheckAndRefresh(request.Context()); e != nil {
			return nil, "", e
		}
		token, e := t.Token(request.Context())
		if e != nil {
			return nil, "", e
		}
		if token == nil {
			return nil, "", errors.New("No claude.ai OAuth token available")
		}
		clone := request.Clone(request.Context())
		clone.Header.Set("Authorization", "Bearer "+*token)
		if retry && request.Body != nil {
			if request.GetBody == nil {
				return nil, "", errors.New("MCP proxy request body cannot be replayed")
			}
			clone.Body, e = request.GetBody()
			if e != nil {
				return nil, "", e
			}
		}
		response, e := t.Inner.RoundTrip(clone)
		return response, *token, e
	}
	response, sent, e := doRequest(false)
	if e != nil || response.StatusCode != 401 {
		return response, e
	}
	changed, _ := t.Handle401(request.Context(), sent)
	if !changed {
		now, tokenError := t.Token(request.Context())
		if tokenError != nil || now == nil || *now == "" || *now == sent {
			return response, nil
		}
	}
	retried, _, retryError := doRequest(true)
	if retryError != nil {
		return response, nil
	}
	_ = response.Body.Close()
	return retried, nil
}
