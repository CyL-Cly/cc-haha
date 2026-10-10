package sessioncontroller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestWritePromiseOwnerCloseSettlesChildWithoutRunningBusiness(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsettled-parent", true: "queued-reaction"}[queued], func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			parent, child := newWriteFuture(), newWriteFuture()
			entered, release := make(chan struct{}), make(chan struct{})
			runDone := make(chan error, 1)
			business := false
			go func() {
				runDone <- owner.Run(context.Background(), func(*cooperative.Scope) error {
					if err := parent.thenOwner(owner, func(*cooperative.Scope, error) { business = true; child.settle(nil) }, child.settle); err != nil {
						return err
					}
					if queued {
						parent.settle(nil)
					}
					close(entered)
					<-release
					return nil
				})
			}()
			awaitSignal(t, entered, "real registered Promise reaction")
			closed := make(chan struct{})
			go func() { owner.Close(); close(closed) }()
			deadline := time.Now().Add(5 * time.Second)
			closing := false
			for time.Now().Before(deadline) {
				if _, err := owner.Submit(context.Background(), cooperative.Read, func(*cooperative.Scope) error { return nil }); errors.Is(err, cooperative.ErrClosed) {
					closing = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			close(release)
			awaitSignal(t, closed, "owner termination")
			if err := <-runDone; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := child.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
				t.Fatal("lost administrative rejection", err)
			}
			if !closing || business {
				t.Fatal("closing gate missing or discarded business reaction executed")
			}
			parent.settle(nil)
			if err := child.Wait(context.Background()); !errors.Is(err, cooperative.ErrClosed) {
				t.Fatal("late parent resurrected child", err)
			}
		})
	}
}
