package sdkhost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestActualAgentIteratorReturnWaitsPendingNextAndOriginalFinally(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "finally", true: "finally-error"}[failure], func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			foreign := cooperative.New()
			defer foreign.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ready := make(chan struct{})
			pending := make(chan struct{})
			finished := make(chan error, 1)
			var host *Host
			var receipt *OwnerFuture[struct{}]
			cleanupError := errors.New("actual iterator finally failed")
			go func() {
				finished <- owner.Run(ctx, func(scope *cooperative.Scope) error {
					bridge, e := newQueryIteratorBridge(scope, owner)
					if e != nil {
						return e
					}
					host = &Host{options: Options{Owner: owner, Print: &PrintOptions{NativeProcess: true, AgentID: "a0123456789abcdef"}}, agentQueryIterator: bridge}
					close(ready)
					if e = scope.AwaitPromise(pending); e != nil {
						return e
					}
					calls := 0
					action, e := bridge.emit(scope, queryloop.Yield{Message: &queryloop.Message{Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"assistant"`)})}}, func(queryloop.Yield) (requestretry.NoticeAction, error) {
						calls++
						if !host.agentIteratorReturnRequested() {
							t.Error("lost original pending return")
						}
						return requestretry.Resume, nil
					})
					if e != nil {
						return e
					}
					if action != requestretry.Stop || calls != 1 {
						t.Errorf("return demand action=%v calls=%d", action, calls)
					}
					finishErr := bridge.finish(scope, queryloop.Outcome{}, nil, func() error {
						if settled(receipt.Done()) {
							t.Error("return completed before finally")
						}
						if failure {
							return cleanupError
						}
						return nil
					})
					if finishErr != nil {
						_ = bridge.agentReturnReceipt.Reject(scope, finishErr)
					} else {
						_ = bridge.agentReturnReceipt.Resolve(scope, struct{}{})
					}
					return finishErr
				})
			}()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if e := foreign.Run(ctx, func(scope *cooperative.Scope) error {
				_, e := host.ReturnAgentIterator(scope)
				if !errors.Is(e, cooperative.ErrRestricted) {
					t.Errorf("foreign owner accepted %v", e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			r, e := owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
				var e error
				receipt, e = host.ReturnAgentIterator(scope)
				return e
			})
			if e == nil {
				e = r.Wait()
			}
			if e != nil {
				t.Fatal(e)
			}
			if settled(receipt.Done()) {
				t.Fatal("pending-next was force completed")
			}
			close(pending)
			select {
			case e := <-finished:
				if failure && !errors.Is(e, cleanupError) || !failure && e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			_, e = receipt.Wait(ctx)
			if failure && !errors.Is(e, cleanupError) || !failure && e != nil {
				t.Fatalf("actual finally return receipt %v", e)
			}
		})
	}
}
