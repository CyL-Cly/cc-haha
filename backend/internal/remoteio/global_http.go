package remoteio

import (
	"io"
	"net/http"
	"sync"
)

// Each global request observes an immutable current environment, including
// proxy bypass and TLS changes. Its owned native connection pool is retired
// when the request body closes, rather than retaining old configuration.
func (s *Service) doGlobalHTTP(request *http.Request) (*http.Response, error) {
	if s.options.GlobalHTTP == nil {
		return s.options.HTTP.Do(request)
	}
	client, err := s.options.GlobalHTTP(s.environmentSnapshot())
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		client.CloseIdleConnections()
		return nil, err
	}
	response.Body = &globalHTTPBody{ReadCloser: response.Body, release: client.CloseIdleConnections}
	return response, nil
}

type globalHTTPBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *globalHTTPBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}
