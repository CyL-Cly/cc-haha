//go:build cch_native_ref_probe

package resumestate

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type heldActualPlanRead struct {
	OSFiles
	entered, allow, done chan struct{}
}

func (f *heldActualPlanRead) ReadFile(p string) ([]byte, error) {
	close(f.entered)
	<-f.allow
	defer close(f.done)
	return f.OSFiles.ReadFile(p)
}

func TestNativeRefReleaseFailureBoundary(t *testing.T) {
	for _, mode := range []string{"physical-failure", "initial-delivery-rejected-while-physical-held"} {
		t.Run(mode, func(t *testing.T) {
			entered, allow, physicalDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			files := &heldActualPlanRead{entered: entered, allow: allow, done: physicalDone}
			service, owner, _, refs := publicServiceReferences(t, files)
			home := service.options.ConfigDir
			before, _ := refs.Snapshot()
			if before != 0 {
				t.Fatal(before)
			}
			path := filepath.Join(home, "source")
			if e := os.MkdirAll(home, 0700); e != nil {
				t.Fatal(e)
			}
			if mode != "physical-failure" {
				if e := os.WriteFile(path, []byte("held-original-plan"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			var future *ownerasync.OwnerFuture[[]byte]
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				var e error
				future, e = service.nativeRead(scope, path)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			<-entered
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(allow) }) }
			t.Cleanup(func() { release(); <-physicalDone })
			count, idle := refs.Snapshot()
			if count != 1 {
				t.Fatal("no original native admission", count)
			}
			if mode == "initial-delivery-rejected-while-physical-held" {
				owner.Close()
				if count, _ := refs.Snapshot(); count != 1 {
					t.Fatal("Owner lifetime released still-held physical work", count)
				}
				_, failure := future.Wait(context.Background())
				if !errors.Is(failure, cooperative.ErrClosed) {
					t.Fatal("original future owner rejection changed", failure)
				}
			}
			release()
			<-physicalDone
			_, failure := future.Wait(context.Background())
			<-idle
			if count, _ := refs.Snapshot(); count != 0 {
				t.Fatal("actual initial-delivery/failure fallback leaked", count)
			}
			if mode == "physical-failure" {
				if !errors.Is(failure, os.ErrNotExist) {
					t.Fatal("original physical errno lost", failure)
				}
			} else if !errors.Is(failure, cooperative.ErrClosed) {
				t.Fatal("late delivery rewrote original future cause", failure)
			}
			owner.Close()
		})
	}
}
