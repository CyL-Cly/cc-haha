package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func lifecycleReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("owned lifecycle event did not arrive")
	}
	var zero T
	return zero
}
func lifecycleServer(t *testing.T) *Server {
	t.Helper()
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		return nil, errors.New("no model in transport test")
	})
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}
func TestAPIResponseAbortKeepsBusinessContextButCancelsExplicitSignal(t *testing.T) {
	s := lifecycleServer(t)
	started, completed := make(chan struct{}), make(chan error, 1)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), remoteBrowserKey{}, true))
	defer cancel()
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/control", nil).WithContext(ctx)
	done := make(chan struct{})
	recorder := httptest.NewRecorder()
	go func() {
		s.serveAPILifecycle(recorder, req, true, func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-requestSignalContext(r).Done()
			if r.Context().Err() != nil {
				completed <- errors.New("request cancellation leaked into ordinary business promise")
				return
			}
			if remote, ok := r.Context().Value(remoteBrowserKey{}).(bool); !ok || !remote {
				completed <- errors.New("trusted renderer/remote context values lost")
				return
			}
			completed <- nil
			writeJSON(w, 200, map[string]bool{"ok": true})
		})
		close(done)
	}()
	lifecycleReceive(t, started)
	cancel()
	lifecycleReceive(t, done)
	if recorder.Code != 499 {
		t.Fatalf("response %d", recorder.Code)
	}
	if e := lifecycleReceive(t, completed); e != nil {
		t.Fatal(e)
	}
}
func TestAPINonRacingCallerWaitsForCommittedOperationLikeOriginalPublicAndSDKListeners(t *testing.T) {
	s := lifecycleServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/session-collaboration/create", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.serveAPILifecycle(w, req, false, func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
			if r.Context().Err() != nil {
				t.Error("direct business operation cancelled")
			}
			writeJSON(w, 201, map[string]bool{"ok": true})
		})
		close(done)
	}()
	lifecycleReceive(t, started)
	cancel()
	select {
	case <-done:
		t.Fatal("non-racing Source caller was converted to response499")
	default:
	}
	close(release)
	lifecycleReceive(t, done)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
}
func TestAPIResponseBridgePreservesPhysicalStreamingBytesAndDisconnectCleanup(t *testing.T) {
	s := lifecycleServer(t)
	payload := bytes.Repeat([]byte("owned-stream-"), 40000)
	tail := []byte("stream-finished")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveAPILifecycle(w, r, true, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(payload)
			_ = http.NewResponseController(w).Flush()
			_, _ = w.Write(tail)
		})
	}))
	defer peer.Close()
	res, e := peer.Client().Get(peer.URL)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := io.ReadAll(res.Body)
	res.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(actual, append(append([]byte(nil), payload...), tail...)) || res.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatal("bounded bridge changed physical stream")
	}
	started, release, operationDone, returned := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveAPILifecycle(w, r, true, func(w http.ResponseWriter, r *http.Request) {
			defer close(operationDone)
			_, _ = w.Write([]byte("ready"))
			_ = http.NewResponseController(w).Flush()
			close(started)
			<-release
			if r.Context().Err() != nil {
				t.Error("stream consumer abort cancelled committed business")
			}
			_, _ = w.Write(tail)
		})
		close(returned)
	}))
	defer blocked.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", blocked.URL, nil)
	response, e := blocked.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data := make([]byte, 5)
	if _, e = io.ReadFull(response.Body, data); e != nil || string(data) != "ready" {
		t.Fatal(e, string(data))
	}
	lifecycleReceive(t, started)
	cancel()
	response.Body.Close()
	lifecycleReceive(t, returned)
	select {
	case <-operationDone:
		t.Fatal("stream body cancellation ended unrelated pending operation")
	default:
	}
	once.Do(func() { close(release) })
	lifecycleReceive(t, operationDone)
}
func TestServerCloseCancelsAndJoinsAcceptedAPIOperationsBeforeNativeShutdown(t *testing.T) {
	s := lifecycleServer(t)
	started, cleanup, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.options.OnShutdown = func(context.Context) error {
		select {
		case <-finished:
			return nil
		default:
			t.Error("native settings released before operation cleanup")
			return nil
		}
	}
	req := httptest.NewRequest("GET", "http://127.0.0.1/api/pending", nil)
	returned := make(chan struct{})
	go func() {
		s.serveAPILifecycle(httptest.NewRecorder(), req, true, func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(cleanup)
			<-release
			close(finished)
		})
		close(returned)
	}()
	lifecycleReceive(t, started)
	closed := make(chan error, 1)
	go func() { closed <- s.Close(context.Background()) }()
	lifecycleReceive(t, cleanup)
	select {
	case <-returned:
		t.Fatal("server stop was incorrectly treated as client response abort")
	default:
	}
	select {
	case e := <-closed:
		t.Fatalf("Close returned before operation joined: %v", e)
	default:
	}
	once.Do(func() { close(release) })
	if e := lifecycleReceive(t, closed); e != nil {
		t.Fatal(e)
	}
	lifecycleReceive(t, returned)
}
