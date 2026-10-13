package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
)

// Runtime replacement may have already fully closed the old Session when
// Desktop Close takes its runtime lock. Closing that actual graph again must
// join its established receipt rather than submit new work to its dead Owner.
func TestDesktopCloseJoinsAlreadyClosedNativeEngineWithoutNewCleanupAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("closing an idle native engine requested a model")
		http.Error(w, "not a model scenario", http.StatusBadRequest)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("isolated test keychain unavailable")
	}
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	t.Cleanup(func() { _ = d.Close(context.Background()) })
	engine := d.currentEngine()
	if err := engine.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-engine.process.Owner.Done():
	default:
		t.Fatal("actual native engine Close did not join its Owner")
	}
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < cap(failures); i++ {
		group.Add(1)
		go func() { defer group.Done(); failures <- d.Close(ctx) }()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatalf("already joined engine close failed: %v", err)
		}
	}
	// Inject an established close-receipt fault after the physical graph has
	// joined; an idempotent desktop Close must preserve that authoritative error.
	prior := errors.New("established native close receipt failed")
	engine.mu.Lock()
	engine.closeError = prior
	engine.mu.Unlock()
	if err := d.Close(ctx); !errors.Is(err, prior) {
		t.Fatalf("authoritative prior close failure was discarded: %v", err)
	}
}
