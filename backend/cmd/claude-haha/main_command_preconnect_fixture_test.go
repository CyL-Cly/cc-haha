package main

import (
	"errors"
	"net/http"
)

// The actual normal command preAction uses its original detached HEAD; this
// external fixture primitive permits only an explicit private loopback peer.
// Default/public API URLs are rejected before any socket operation.
func nativeCommandFixturePreconnect(request *http.Request, actual *http.Client) (*http.Response, error) {
	allowed := func(request *http.Request) bool {
		return request != nil && request.Method == http.MethodHead && request.URL != nil && request.URL.Hostname() == "127.0.0.1" && request.URL.Port() != "" && request.URL.User == nil && (request.URL.Scheme == "http" || request.URL.Scheme == "https")
	}
	if actual == nil || !allowed(request) {
		return nil, errors.New("isolated normal command preconnect requires exact loopback HEAD")
	}
	client := *actual
	client.CheckRedirect = func(next *http.Request, previous []*http.Request) error {
		if !allowed(next) {
			return errors.New("isolated normal command preconnect redirect escapes loopback")
		}
		if actual.CheckRedirect != nil {
			return actual.CheckRedirect(next, previous)
		}
		return nil
	}
	return client.Do(request)
}
