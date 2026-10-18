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

func transferredPoolFetch(ctx context.Context, owner *cooperative.Owner, pool *Pool, url, auth string) error {
	return owner.Run(ctx, func(scope *cooperative.Scope) error {
		driver, err := pool.Driver(owner, scope, nil)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("actual owned request"))
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", auth)
		response, err := driver.OpenFetch(request, &provider.RawHTTP1Exchange{})
		if err != nil {
			return err
		}
		defer response.Response.Body.Close()
		body, err := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return scope.AwaitBytes(cooperative.StreamNext, work)
		})
		if err != nil {
			return err
		}
		value, err := body.ReadFetchBody(response.Response.Body)
		if err == nil && string(value) != "actual complete response" {
			return errors.New("actual response body changed")
		}
		return err
	})
}

func TestNativeHTTPPoolTransferUsesChildOwnerAndPreservesParentPhysicalResource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var ports, auth []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		ports = append(ports, r.RemoteAddr)
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = io.WriteString(w, "actual complete response")
	}))
	defer peer.Close()
	parent, child := cooperative.New(), cooperative.New()
	defer parent.Close()
	defer child.Close()
	var root, borrowed *Pool
	var transfer *PoolTransfer
	var expired *cooperative.Scope
	if err := parent.Run(ctx, func(scope *cooperative.Scope) error {
		expired = scope
		var err error
		root, err = NewPool(parent, scope)
		if err == nil {
			transfer, err = root.TransferOnOwner(parent, scope)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if !transfer.SourceOwns(parent) || transfer.SourceOwns(child) {
		t.Fatal("actual parent transfer identity")
	}
	if _, err := root.TransferOnOwner(parent, expired); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("expired parent transfer admitted", err)
	}
	if err := child.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := root.TransferOnOwner(child, scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign parent transfer admitted", err)
		}
		if _, err := root.Driver(child, scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("old parent pool admitted foreign child", err)
		}
		if _, err := transfer.BindOnOwner(parent, scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign scope admitted into target binding", err)
		}
		if _, err := (*PoolTransfer)(nil).BindOnOwner(child, scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("nil transfer admitted", err)
		}
		if _, err := (&PoolTransfer{}).BindOnOwner(child, scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("unminted transfer admitted", err)
		}
		var err error
		borrowed, err = transfer.BindOnOwner(child, scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := transferredPoolFetch(ctx, parent, root, peer.URL, "Bearer parent-only"); err != nil {
		t.Fatal(err)
	}
	if err := transferredPoolFetch(ctx, child, borrowed, peer.URL, "Bearer child-only"); err != nil {
		t.Fatal(err)
	}
	if err := borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := borrowed.Driver(child, scope, nil); !errors.Is(err, cooperative.ErrClosed) {
			t.Error("closed child binding admitted a request", err)
		}
		if _, err := borrowed.TransferOnOwner(child, scope); !errors.Is(err, cooperative.ErrClosed) {
			t.Error("closed child binding transferred", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := transferredPoolFetch(ctx, parent, root, peer.URL, "Bearer parent-restored"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ports) != 3 || ports[0] != ports[1] || ports[1] != ports[2] || strings.Join(auth, ",") != "Bearer parent-only,Bearer child-only,Bearer parent-restored" {
		t.Fatal("child close lost process pool or leaked request authentication", ports, auth)
	}
}

func TestNativeHTTPPoolTransferSharesStickyClientOptionsAndRootClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var ports []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		ports = append(ports, r.RemoteAddr)
		mu.Unlock()
		_, _ = io.WriteString(w, "actual complete response")
	}))
	defer peer.Close()
	parent, child := cooperative.New(), cooperative.New()
	defer parent.Close()
	defer child.Close()
	var root, borrowed *Pool
	var transfer *PoolTransfer
	if err := parent.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		root, err = NewPool(parent, scope)
		if err == nil {
			transfer, err = root.TransferOnOwner(parent, scope)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := child.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		borrowed, err = transfer.BindOnOwner(child, scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := transferredPoolFetch(ctx, parent, root, peer.URL, "parent"); err != nil {
		t.Fatal(err)
	}
	if err := child.Run(ctx, func(scope *cooperative.Scope) error {
		return NewPooledFactory(child, scope, nil, borrowed).NativeKeepAliveDisable()()
	}); err != nil {
		t.Fatal(err)
	}
	if err := borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := transferredPoolFetch(ctx, parent, root, peer.URL, "parent after child options"); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	observed := append([]string(nil), ports...)
	mu.Unlock()
	if len(observed) != 3 || observed[0] == observed[1] || observed[1] == observed[2] {
		t.Fatal("child sticky keepalive flag was lost on return to parent", observed)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := root.TransferOnOwner(parent, scope); !errors.Is(err, cooperative.ErrClosed) {
			t.Error("closed process pool transferred", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := child.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := transfer.BindOnOwner(child, scope); !errors.Is(err, cooperative.ErrClosed) {
			t.Error("old transfer resurrected closed process pool", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ports) != 3 {
		t.Fatal("guard performed actual network I/O", ports)
	}
}

func TestNativeHTTPPoolTransferChildCancellationAndGlobalCloseJoinRealBodies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := map[string]chan struct{}{"/held-cancel": make(chan struct{}), "/held-close": make(chan struct{})}
	closed := map[string]chan struct{}{"/held-cancel": make(chan struct{}), "/held-close": make(chan struct{})}
	var mu sync.Mutex
	var ports []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		ports = append(ports, r.RemoteAddr)
		mu.Unlock()
		if signal := started[r.URL.Path]; signal != nil {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "physical partial body")
			w.(http.Flusher).Flush()
			close(signal)
			<-r.Context().Done()
			close(closed[r.URL.Path])
			return
		}
		_, _ = io.WriteString(w, "actual complete response")
	}))
	defer peer.Close()
	parent, child := cooperative.New(), cooperative.New()
	defer parent.Close()
	defer child.Close()
	var root, borrowed *Pool
	var transfer *PoolTransfer
	if err := parent.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		root, err = NewPool(parent, scope)
		if err == nil {
			transfer, err = root.TransferOnOwner(parent, scope)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	bind := func() {
		t.Helper()
		if err := child.Run(ctx, func(scope *cooperative.Scope) error {
			var err error
			borrowed, err = transfer.BindOnOwner(child, scope)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	await := func(signal <-chan struct{}, phase string) {
		t.Helper()
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatal(phase, ctx.Err())
		}
	}
	bind()
	if err := transferredPoolFetch(ctx, parent, root, peer.URL+"/initial", "parent"); err != nil {
		t.Fatal(err)
	}
	childContext, stopChild := context.WithCancel(ctx)
	defer stopChild()
	done := make(chan error, 1)
	go func() { done <- transferredPoolFetch(childContext, child, borrowed, peer.URL+"/held-cancel", "child") }()
	await(started["/held-cancel"], "actual child body")
	if err := transferredPoolFetch(ctx, parent, root, peer.URL+"/during-child", "parent independently active"); err != nil {
		t.Fatal(err)
	}
	stopChild()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("actual canceled child body", err)
		}
	case <-ctx.Done():
		t.Fatal("child read did not join", ctx.Err())
	}
	await(closed["/held-cancel"], "actual child socket close")
	if err := borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := transferredPoolFetch(ctx, parent, root, peer.URL+"/after-child", "parent restored"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	observed := append([]string(nil), ports...)
	mu.Unlock()
	if len(observed) != 4 || observed[0] != observed[1] || observed[1] == observed[2] || observed[2] != observed[3] {
		t.Fatal("child cancellation closed unrelated parent transport", observed)
	}
	bind()
	go func() {
		done <- transferredPoolFetch(ctx, child, borrowed, peer.URL+"/held-close", "child global close")
	}()
	await(started["/held-close"], "second actual child body")
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("global close completed an unfinished body")
		}
	case <-ctx.Done():
		t.Fatal("global close could not join child read", ctx.Err())
	}
	await(closed["/held-close"], "global actual socket close")
	if err := child.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := borrowed.Driver(child, scope, nil); !errors.Is(err, cooperative.ErrClosed) {
			t.Error("root close left child binding usable", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
