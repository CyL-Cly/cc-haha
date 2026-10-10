package socket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Open the actual FrameSocket/read pump against a private physical WebSocket.
// The server only drains the connection; it cannot send network or app events.
func privateDisconnectSocket(t *testing.T) (*FrameSocket, <-chan struct{}, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	closed := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(ctx)
	}))
	fs := NewFrameSocket(waLog.Noop, peer.Client())
	fs.URL = peer.URL
	if err := fs.Connect(ctx); err != nil {
		cancel()
		peer.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		fs.Close(-1)
		cancel()
		peer.Close()
	})
	return fs, closed, ctx
}

func waitPrivateDisconnect(t *testing.T, closed <-chan struct{}, ctx context.Context) {
	t.Helper()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("physical WebSocket peer did not close", ctx.Err())
	}
}

func TestNoiseDisconnectCallbackCloseRaces(t *testing.T) {
	for _, mode := range []string{"stop", "constructor"} {
		t.Run(mode, func(t *testing.T) {
			for round := 0; round < 32; round++ {
				func() {
					fs, closed, ctx := privateDisconnectSocket(t)
					var callbacks atomic.Int32
					handler := func(context.Context, *NoiseSocket, bool) { callbacks.Add(1) }
					var ns *NoiseSocket
					if mode == "stop" {
						var err error
						ns, err = newNoiseSocket(nil, fs, nil, nil, nil, handler)
						if err != nil {
							t.Fatal(err)
						}
					}
					start := make(chan struct{})
					var joined sync.WaitGroup
					joined.Add(2)
					go func() {
						defer joined.Done()
						<-start
						fs.Close(-1)
					}()
					go func() {
						defer joined.Done()
						<-start
						if mode == "stop" {
							ns.Stop(false, false)
						} else {
							created, err := newNoiseSocket(nil, fs, nil, nil, nil, handler)
							if err != nil {
								t.Error(err)
								return
							}
							created.Stop(false, true)
						}
					}()
					close(start)
					joined.Wait()
					waitPrivateDisconnect(t, closed, ctx)
					// Whichever callback registration/clear linearizes before
					// Close decides whether the single callback is dispatched.
					if callbacks.Load() > 1 {
						t.Fatal("disconnect callback was replayed", callbacks.Load())
					}
				}()
			}
		})
	}
}

func TestNoiseStopSuppressesCallbackBeforePhysicalClose(t *testing.T) {
	fs, closed, ctx := privateDisconnectSocket(t)
	var callbacks atomic.Int32
	ns, err := newNoiseSocket(nil, fs, nil, nil, nil, func(context.Context, *NoiseSocket, bool) { callbacks.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	ns.Stop(true, false)
	waitPrivateDisconnect(t, closed, ctx)
	fs.lock.Lock()
	callbackCleared := fs.OnDisconnect == nil
	fs.lock.Unlock()
	if !callbackCleared || callbacks.Load() != 0 {
		t.Fatal("manual Stop emitted a suppressed disconnect callback", callbackCleared, callbacks.Load())
	}
}

func TestFrameCloseCapturesStableCallbackBeforeNoiseStop(t *testing.T) {
	fs, closed, ctx := privateDisconnectSocket(t)
	entered, release, delivered := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
	ns, err := newNoiseSocket(nil, fs, nil, nil, nil, func(callbackContext context.Context, _ *NoiseSocket, remote bool) {
		close(entered)
		<-release
		if callbackContext != ctx {
			t.Error("disconnect lost the original socket context")
		}
		delivered <- remote
	})
	if err != nil {
		t.Fatal(err)
	}
	fs.Close(-1)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("registered disconnect callback was not dispatched")
	}
	ns.Stop(false, false)
	fs.Close(0)
	close(release)
	select {
	case remote := <-delivered:
		if remote {
			t.Fatal("manual close changed to a remote disconnect")
		}
	case <-ctx.Done():
		t.Fatal("captured callback did not finish after Stop cleared registration")
	}
	waitPrivateDisconnect(t, closed, ctx)
}
