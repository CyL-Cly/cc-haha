package whatsappcandidate

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestMediaPeerCloseJoinsHijackedHandlerAndActualSourceTempDir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	handlers := newMediaFixtureHandlers()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var admitted atomic.Int32
	artifacts := make(chan string, 1)
	failures := make(chan error, 1)
	peer := httptest.NewTLSServer(handlers.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		admitted.Add(1)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			failures <- err
			return
		}
		defer conn.CloseNow()
		close(entered)
		// The real handler is already hijacked but deliberately has not yet
		// called t.TempDir or started the original Source subprocess.
		<-release
		result := actualMediaSource(t, map[string]any{
			"op": "retryNotification", "id": "OWNED_JOIN_REGRESSION",
			"key":  base64.StdEncoding.EncodeToString(make([]byte, 32)),
			"path": "/private-joined", "result": 1,
		})
		if len(nodeBytes(result["ciphertext"])) == 0 || len(nodeBytes(result["iv"])) != 12 {
			failures <- os.ErrInvalid
			return
		}
		artifact := filepath.Join(t.TempDir(), "joined-source-result.json")
		if err := os.WriteFile(artifact, nodeJSON(result), 0600); err != nil {
			failures <- err
			return
		}
		artifacts <- artifact
	})))
	var client *websocket.Conn
	defer func() {
		releaseOnce.Do(func() { close(release) })
		if client != nil {
			_ = client.CloseNow()
		}
		cancel()
		<-handlers.close(peer)
		// Independent physical cleanup also covers the intentionally broken
		// old counterfactual, whose receipt omits the join. Never leak its child.
		handlers.active.Wait()
	}()
	var err error
	client, _, err = websocket.Dial(ctx, "wss"+peer.URL[len("https"):], &websocket.DialOptions{HTTPClient: peer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case err := <-failures:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("owned WebSocket handler did not enter", ctx.Err())
	}
	_ = client.CloseNow()
	joined := handlers.close(peer)
	select {
	case <-joined:
		t.Fatal("peer Close returned while its hijacked handler can still create TempDir and Source child")
	default:
	}
	// Stop admission before Wait: there can be no WaitGroup Add-after-Wait
	// race and no new test-side fixture operation after this receipt starts.
	rejected := httptest.NewRecorder()
	handlers.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("fixture admitted a handler after close")
	})).ServeHTTP(rejected, httptest.NewRequest(http.MethodGet, "https://fixture.invalid/", nil))
	if rejected.Code != http.StatusServiceUnavailable || admitted.Load() != 1 {
		t.Fatal("close did not stop handler admission", rejected.Code, admitted.Load())
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("Close did not join actual Source child and physical artifact", ctx.Err())
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	select {
	case path := <-artifacts:
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			t.Fatal("joined physical Source artifact absent", err)
		}
	default:
		t.Fatal("handler completion preceded its actual Source subprocess/artifact")
	}
}
