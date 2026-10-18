package clibootstrap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeInteractiveSpinnerObserverRejectsForeignExpiredFiniteAndClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Error("Read-only spinner guards invoked a model")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer peer.Close()
	var calls atomic.Int32
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
		options.Interactive = true
		options.OnTerminalSpinner = func(tui.TerminalSpinnerProgress) error { calls.Add(1); return nil }
	})
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		return m.observeNativeTerminalSpinner(scope, queryloop.Yield{RequestStart: true})
	}); err != nil || calls.Load() != 1 {
		t.Fatal("Actual initialized root observer was not bound", err, calls.Load())
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.observeNativeTerminalSpinner(scope, queryloop.Yield{RequestStart: true}); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("Spinner observer accepted a foreign Scope")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var expired *cooperative.Scope
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error { expired = scope; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []*cooperative.Scope{nil, expired} {
		if err := m.observeNativeTerminalSpinner(scope, queryloop.Yield{RequestStart: true}); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal("Spinner observer accepted a missing or expired Scope", err)
		}
	}
	finite := &MainPrint{Owner: foreign, options: MainPrintOptions{OnTerminalSpinner: func(tui.TerminalSpinnerProgress) error { calls.Add(1); return nil }}}
	if finite.nativeTerminalSpinnerObserver() != nil {
		t.Fatal("Finite Main installed an interactive observer")
	}
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if err := finite.observeNativeTerminalSpinner(scope, queryloop.Yield{RequestStart: true}); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("Finite Main accepted interactive progress")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	if err := m.observeNativeTerminalSpinner(expired, queryloop.Yield{RequestStart: true}); !errors.Is(err, cooperative.ErrRestricted) || calls.Load() != 1 {
		t.Fatal("Closed root observer admitted late progress", err, calls.Load())
	}
}

func TestActualNativeInteractiveSpinnerRequestCancellationJoinsItsOriginalModel(t *testing.T) {
	entered, aborted := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.ReadAll(request.Body)
		close(entered)
		<-request.Context().Done()
		close(aborted)
	}))
	defer peer.Close()
	var starts atomic.Int32
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) {
		options.Interactive = true
		options.OnTerminalSpinner = func(progress tui.TerminalSpinnerProgress) error {
			if progress.Mode == "requesting" {
				starts.Add(1)
			}
			return nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	turnCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(turnCtx, mainObj([]byte(`{"type":"user","message":{"role":"user","content":"Actual held spinner request"}}`)))
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if starts.Load() != 1 {
		t.Fatal("Actual RequestStart did not reach the terminal before the held request", starts.Load())
	}
	stop()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal("Original canceled query did not settle", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Spinner observer retained the original canceled query")
	}
	select {
	case <-aborted:
	case <-time.After(3 * time.Second):
		t.Fatal("Actual held provider request was not canceled")
	}
	m.Close()
}
