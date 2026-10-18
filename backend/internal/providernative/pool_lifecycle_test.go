//go:build darwin

package providernative

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestNativeHTTPPoolHeldHeadUnconsumedBodyCancellationAndOwnerGuards(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	headStarted, headCancelled := make(chan struct{}), make(chan struct{})
	bodyCancelled := make(chan struct{})
	var mu sync.Mutex
	var ports []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ports = append(ports, r.RemoteAddr)
		mu.Unlock()
		if r.Method == http.MethodHead {
			close(headStarted)
			<-r.Context().Done()
			close(headCancelled)
			return
		}
		if r.URL.Path == "/held" {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, "partial")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(bodyCancelled)
			return
		}
		_, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, "complete")
	}))
	defer peer.Close()
	owner := cooperative.New()
	defer owner.Close()
	var pool *Pool
	var expired *cooperative.Scope
	if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
		expired = scope
		var e error
		pool, e = NewPool(owner, scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if _, e := pool.Driver(owner, expired, nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("expired pool owner scope admitted", e)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if _, e := pool.Driver(foreign, scope, nil); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("foreign pool driver", e)
		}
		if _, e := pool.HeadTransport(owner, scope, &provider.RawHTTP1Exchange{}); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("foreign pool HEAD", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var transport http.RoundTripper
	if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		transport, e = pool.HeadTransport(owner, scope, &provider.RawHTTP1Exchange{})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	headContext, stopHead := context.WithCancel(ctx)
	headDone := make(chan error, 1)
	go func() {
		request, _ := http.NewRequestWithContext(headContext, http.MethodHead, peer.URL, nil)
		r, e := transport.RoundTrip(request)
		if r != nil {
			_ = r.Body.Close()
		}
		headDone <- e
	}()
	select {
	case <-headStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A genuine held init HEAD owns one socket; the model must independently
	// acquire another, rather than serialize behind an unavailable idle lease.
	var held io.ReadCloser
	if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
		driver, e := pool.Driver(owner, scope, nil)
		if e != nil {
			return e
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+"/held", strings.NewReader("actual-model"))
		result, e := driver.OpenFetch(request, &provider.RawHTTP1Exchange{})
		if e != nil {
			return e
		}
		held = result.Response.Body
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if held == nil {
		t.Fatal("held HEAD blocked actual model headers")
	}
	_ = held.Close()
	select {
	case <-bodyCancelled:
	case <-ctx.Done():
		t.Fatal("unconsumed body did not close actual socket", ctx.Err())
	}
	if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
		driver, e := pool.Driver(owner, scope, nil)
		if e != nil {
			return e
		}
		for range 2 {
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL+"/complete", strings.NewReader("actual-next"))
			result, e := driver.OpenFetch(request, &provider.RawHTTP1Exchange{})
			if e != nil {
				return e
			}
			body, e := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return scope.AwaitBytes(cooperative.StreamNext, work)
			})
			if e != nil {
				return e
			}
			data, e := body.ReadFetchBody(result.Response.Body)
			_ = result.Response.Body.Close()
			if e != nil {
				return e
			}
			if string(data) != "complete" {
				t.Error("actual next body", string(data))
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	actual := append([]string{}, ports...)
	mu.Unlock()
	if len(actual) != 4 || actual[0] == actual[1] || actual[1] == actual[2] || actual[2] != actual[3] {
		t.Fatal("held/unconsumed/complete resource reuse", actual)
	}
	stopHead()
	select {
	case e := <-headDone:
		if e == nil {
			t.Fatal("held HEAD cancellation disappeared")
		}
	case <-ctx.Done():
		t.Fatal("HEAD worker did not join")
	}
	select {
	case <-headCancelled:
	case <-ctx.Done():
		t.Fatal("held HEAD socket survived cancellation")
	}
	_ = pool.Close()
	if e := owner.Run(ctx, func(scope *cooperative.Scope) error {
		driver, e := pool.Driver(owner, scope, nil)
		if e != nil {
			return e
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, peer.URL, nil)
		if _, e = driver.OpenFetch(request, &provider.RawHTTP1Exchange{}); e == nil {
			t.Error("closed pool admitted new connection")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ports) != 4 {
		t.Fatal("closed pool performed transport", ports)
	}
}
