package analytics

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"reflect"
	"testing"
)

func TestPublicAnalyticsFacadeRetainsOriginalQueuedObjectsAndFirstSink(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	facade, err := NewPublicFacade(owner, func(*cooperative.Scope) map[string]string { return map[string]string{"DISABLE_TELEMETRY": "1"} })
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	var retained *cooperative.Scope
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		retained = scope
		sync := protocol.NewJSONObject(field("value", 1))
		async := protocol.NewJSONObject(field("value", 2))
		if err := facade.Log(scope, Event{Name: "sync", Metadata: sync}); err != nil {
			return err
		}
		queued, err := facade.LogAsync(scope, Event{Name: "async", Metadata: async})
		if err != nil {
			return err
		}
		if _, err := queued.Await(scope); err != nil {
			return err
		}
		sync.Set("value", scalar(41))
		sync.Set("added", scalar(true))
		async.Set("value", scalar(42))
		sink := &PublicSink{Log: func(_ *cooperative.Scope, event Event) error {
			seen = append(seen, event.Name+":"+string(event.Metadata.Get("value")))
			if event.Name == "sync" && string(event.Metadata.Get("added")) != "true" {
				t.Error("queued metadata lost its original outer object")
			}
			return nil
		}}
		sink.LogAsync = func(next *cooperative.Scope, event Event) (*ownerasync.OwnerFuture[struct{}], error) {
			seen = append(seen, event.Name+":"+string(event.Metadata.Get("value")))
			result, err := ownerasync.NewOwnerFuture[struct{}](owner)
			if err != nil {
				return nil, err
			}
			return result, result.Resolve(next, struct{}{})
		}
		if err := facade.Attach(scope, sink); err != nil {
			return err
		}
		if len(seen) != 0 {
			return fmt.Errorf("analytics queue drained on Attach's synchronous stack: %v", seen)
		}
		if err := facade.Attach(scope, &PublicSink{Log: func(*cooperative.Scope, Event) error { t.Error("first sink replaced"); return nil }, LogAsync: sink.LogAsync}); err != nil {
			return err
		}
		if err := scope.AwaitFulfilled(); err != nil {
			return err
		}
		if !reflect.DeepEqual(seen, []string{"sync:41", "async:42"}) {
			return fmt.Errorf("Source queued insertion order/values changed: %v", seen)
		}
		return facade.Close(scope)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := facade.Log(retained, Event{Name: "expired"}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("expired public facade admission", err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := facade.Log(scope, Event{Name: "closed"}); !errors.Is(err, cooperative.ErrClosed) {
			return fmt.Errorf("closed public facade: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := facade.Attach(scope, &PublicSink{}); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign facade sink: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicAnalyticsAsyncAfterAttachAdoptsActualSinkCompletion(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	facade, _ := NewPublicFacade(owner, func(*cooperative.Scope) map[string]string { return map[string]string{} })
	failure := errors.New("actual attached async failure")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		backend, err := ownerasync.NewOwnerFuture[struct{}](owner)
		if err != nil {
			return err
		}
		if err := facade.Attach(scope, &PublicSink{Log: func(*cooperative.Scope, Event) error { return nil }, LogAsync: func(*cooperative.Scope, Event) (*ownerasync.OwnerFuture[struct{}], error) { return backend, nil }}); err != nil {
			return err
		}
		public, err := facade.LogAsync(scope, Event{Name: "live"})
		if err != nil {
			return err
		}
		if public.Identity() == backend.Identity() {
			return errors.New("async public return reused the sink promise")
		}
		if err := backend.Reject(scope, failure); err != nil {
			return err
		}
		if _, err := public.Await(scope); err != failure {
			return fmt.Errorf("original async rejection changed: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
