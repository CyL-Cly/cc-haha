package imagegeneration

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	weburl "github.com/nlnwa/whatwg-url/url"
)

func (s *Service) downloadGrok(ctx context.Context, address string) ([]byte, error) {
	u, err := weburl.Parse(address)
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme() != "https" || !(host == "x.ai" || strings.HasSuffix(host, ".x.ai")) {
		return nil, errors.New("Grok returned an untrusted image download URL.")
	}
	response, err := s.options.Fetch(ctx, Request{URL: u.Href(false), Method: "GET", Headers: http.Header{}, RedirectError: true})
	if err != nil {
		return nil, err
	}
	if err = upstreamSuccess(response, "Grok image download", nil); err != nil {
		return nil, err
	}
	if length, err := strconv.ParseFloat(response.Headers.Get("content-length"), 64); err == nil && length > MaxGeneratedBytes {
		return nil, errors.New("The generated image exceeded the 30 MB size limit.")
	}
	if response.ReadError != nil {
		return nil, response.ReadError
	}
	if len(response.Body) == 0 || len(response.Body) > MaxGeneratedBytes {
		return nil, errors.New("The generated image was empty or too large.")
	}
	return response.Body, nil
}
