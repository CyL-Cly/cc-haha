//go:build darwin

package providernative

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestNativeHeadInitializationFollowsStrictPrimitiveAndDoesNotAwaitHeaders(t *testing.T) {
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	guarded, allow := make(chan struct{}), make(chan struct{})
	headStarted, headCanceled := make(chan struct{}), make(chan struct{})
	var heads atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead || request.URL.Path != "/original-init" {
			t.Error("actual HEAD contract", request.Method, request.URL)
		}
		if heads.Add(1) == 1 {
			close(headStarted)
		}
		<-request.Context().Done()
		close(headCanceled)
	}))
	defer peer.Close()
	owner := cooperative.New()
	defer owner.Close()
	var pool *Pool
	var client *http.Client
	var initialization *HeadInitialization
	var request *http.Request
	var expired *cooperative.Scope
	requestContext, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		expired = scope
		var err error
		pool, err = NewPool(owner, scope)
		if err != nil {
			return err
		}
		transport, err := pool.HeadTransport(owner, scope, &provider.RawHTTP1Exchange{})
		if err != nil {
			return err
		}
		client = &http.Client{Transport: transport}
		request, err = http.NewRequestWithContext(requestContext, http.MethodHead, peer.URL+"/original-init", nil)
		if err != nil {
			return err
		}
		request, initialization, err = BindHeadInitialization(owner, scope, client, request)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	finished := make(chan struct{})
	failure := make(chan error, 1)
	go func() {
		defer close(finished)
		// The actual external primitive's confinement check precedes client.Do.
		if request.URL.String() != peer.URL+"/original-init" {
			failure <- errors.New("private primitive rejected")
			return
		}
		close(guarded)
		select {
		case <-allow:
		case <-request.Context().Done():
			failure <- request.Context().Err()
			return
		}
		response, err := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		failure <- err
	}()
	if _, err := initialization.Await(expired, finished); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("expired initialization scope admitted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if _, _, err := BindHeadInitialization(foreign, scope, client, request); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign initialization transport admitted", err)
		}
		if _, err := initialization.Await(scope, finished); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign initialization receipt consumed", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waiting := make(chan error, 1)
	go func() {
		waiting <- owner.Run(ctx, func(scope *cooperative.Scope) error {
			admitted, err := initialization.Await(scope, finished)
			if err == nil && !admitted {
				return errors.New("real native reservation never admitted")
			}
			return err
		})
	}()
	select {
	case <-guarded:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-initialization.native.Done():
		t.Fatal("receipt settled before strict primitive entered native transport")
	default:
	}
	if heads.Load() != 0 {
		t.Fatal("HEAD escaped held strict primitive", heads.Load())
	}
	close(allow)
	select {
	case err := <-waiting:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("native launch receipt awaited held headers", ctx.Err())
	}
	select {
	case <-headStarted:
	case <-ctx.Done():
		t.Fatal("actual packet never reached peer", ctx.Err())
	}
	select {
	case <-finished:
		t.Fatal("held response unexpectedly settled fetch")
	default:
	}
	if err := owner.Run(ctx, func(*cooperative.Scope) error { return nil }); err != nil {
		t.Fatal("held headers blocked subsequent actual owner handler", err)
	}
	cancelRequest()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("held native HEAD did not cancel/join", ctx.Err())
	}
	if err := <-failure; err == nil {
		t.Fatal("held HEAD cancellation was lost")
	}
	select {
	case <-headCanceled:
	case <-ctx.Done():
		t.Fatal("actual held HEAD socket survived", ctx.Err())
	}
}

func TestNativeHeadInitializationRejectedPrimitiveAndZeroReceiptCannotAdmit(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var heads atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { heads.Add(1); w.WriteHeader(204) }))
	defer peer.Close()
	var pool *Pool
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		pool, err = NewPool(owner, scope)
		if err != nil {
			return err
		}
		transport, err := pool.HeadTransport(owner, scope, &provider.RawHTTP1Exchange{})
		if err != nil {
			return err
		}
		client := &http.Client{Transport: transport}
		request, _ := http.NewRequestWithContext(ctx, http.MethodHead, peer.URL+"/denied", nil)
		_, initialization, err := BindHeadInitialization(owner, scope, client, request)
		if err != nil {
			return err
		}
		finished := make(chan struct{})
		strictPrimitive := func(request *http.Request) error {
			if request.URL.String() != peer.URL+"/allowed" {
				return os.ErrPermission
			}
			response, err := client.Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
			return err
		}
		rejection := strictPrimitive(request)
		if !errors.Is(rejection, os.ErrPermission) {
			t.Error("strict actual primitive did not reject forbidden target", rejection)
		}
		close(finished)
		if admitted, err := initialization.Await(scope, finished); err != nil || admitted {
			t.Error("rejected primitive claimed native request admission", admitted, err)
		}
		if _, err := new(HeadInitialization).Await(scope, finished); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("zero/unbound receipt admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if heads.Load() != 0 {
		t.Fatal("rejected primitive performed actual HTTP", heads.Load())
	}
}
