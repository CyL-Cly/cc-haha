package clibootstrap

import (
	"bytes"
	"context"
	"net/http"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
)

// Each real init caller owns its once gate, safe environment, TLS and pool.
// Only their detached physical HEAD shares this dispatch/catch boundary.
func dispatchNativeAPIPreconnectOnOwner(scope *cooperative.Scope, owner *cooperative.Owner, state *nativeAPIPreconnectState, target string, client *http.Client, primitive func(*http.Request, *http.Client) (*http.Response, error), binding *ownerasync.NativeBinding) error {
	if !scope.BelongsTo(owner) || !binding.Owns(owner) || state == nil || client == nil || target == "" {
		return cooperative.ErrRestricted
	}
	client.CheckRedirect = func(_ *http.Request, previous []*http.Request) error {
		if len(previous) >= 20 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	fetch := client.Do
	if primitive != nil {
		fetch = func(request *http.Request) (*http.Response, error) { return primitive(request, client) }
	}
	done := make(chan struct{})
	state.mu.Lock()
	state.done = done
	state.mu.Unlock()
	// Original AbortSignal.timeout is armed by init before fetch dispatch.
	requestContext, cancel := context.WithTimeout(state.life.context(), 10*time.Second)
	request, err := http.NewRequestWithContext(requestContext, http.MethodHead, target, bytes.NewReader(nil))
	if err != nil {
		cancel()
		close(done)
		return nil // original detached fetch rejection is caught
	}
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	request.Header.Set("Connection", "keep-alive")
	request.Header.Set("User-Agent", "Bun/1.3.14")
	request, initialization, err := providernative.BindHeadInitialization(owner, scope, client, request)
	if err != nil {
		cancel()
		close(done)
		return err
	}
	work, err := nativeOwnedWork(owner, binding, scope, func() (struct{}, error) {
		defer close(done)
		defer client.CloseIdleConnections()
		defer cancel()
		response, err := fetch(request)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return struct{}{}, err
	})
	if err != nil {
		cancel()
		close(done)
		return err
	}
	_, err = work.Then(scope, func(*cooperative.Scope, struct{}, error) error { return nil })
	if err == nil && initialization != nil {
		_, err = initialization.Await(scope, done)
	}
	return err
}
