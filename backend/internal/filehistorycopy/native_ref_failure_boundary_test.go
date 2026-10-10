//go:build cch_native_ref_probe

package filehistorycopy

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

func TestNativeRefReleaseFailureBoundary(t *testing.T) {
	for _, mode := range []string{"physical-failure", "initial-delivery-rejected-while-physical-held"} {
		t.Run(mode, func(t *testing.T) {
			owner, service, _, refs, home, _, _ := setup(t, OSFiles{})
			before, _ := refs.Snapshot()
			if before != 0 {
				t.Fatal(before)
			}
			entered, allow, physicalDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			source, target := filepath.Join(home, "source"), filepath.Join(home, "target")
			if mode != "physical-failure" {
				if e := os.WriteFile(source, []byte("held-original-copy"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			var future *ownerasync.OwnerFuture[struct{}]
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				var e error
				future, e = service.dispatch(scope, func() error {
					close(entered)
					<-allow
					defer close(physicalDone)
					return (OSFiles{}).CopyFile(source, target)
				})
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
