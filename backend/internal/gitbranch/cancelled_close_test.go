package gitbranch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestCancelledObserverCloseStillStopsAndJoinsActualNativeWatchers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "HEAD")
	if err := os.WriteFile(path, []byte("ref: refs/heads/owned\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service, owner, watch := newActualWatchService(t, root, nil)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		service.branch = &cacheEntry{value: raw("owned")}
		return service.watch(scope, path, false)
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("caller cancellation must remain observable: %v", err)
	}
	joined := make(chan struct{})
	go func() { watch.Join(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		// Recover this test's physical subscription before reporting the old
		// failure, so a failing regression cannot leave a polling goroutine.
		_ = owner.Run(context.Background(), service.Cleanup)
		<-joined
		t.Error("cancelled Close skipped native unwatch; host Join could never finish")
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := service.Branch(scope)
		if !errors.Is(err, cooperative.ErrClosed) {
			t.Errorf("administratively closed service reopened: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
