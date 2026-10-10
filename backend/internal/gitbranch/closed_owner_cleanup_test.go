package gitbranch

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAlreadyClosedOwnerCloseStopsActualWatchResourcesWithoutReopeningPolicy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "HEAD")
	if err := os.WriteFile(path, []byte("ref: refs/heads/closed-owner\n"), 0600); err != nil {
		t.Fatal(err)
	}
	service, owner, watch := newActualWatchService(t, root, nil)
	var original *cacheEntry
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		original = &cacheEntry{value: raw("closed-owner")}
		service.branch = original
		return service.watch(scope, path, false)
	}); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	if err := service.Close(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("lost closed Owner authority", err)
	}
	done := make(chan struct{})
	go func() { watch.Join(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		// Only already allocated native stop closures can recover an old failure.
		for _, stop := range service.watched {
			stop()
		}
		<-done
		t.Fatal("closed Owner left actual native Watch loop alive")
	}
	if service.branch != original || service.closed {
		t.Fatal("administrative resource close mutated Owner policy")
	}
	if err := service.Close(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("repeated close authority", err)
	}
}
