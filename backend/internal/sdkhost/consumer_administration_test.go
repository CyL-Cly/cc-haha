package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
)

func administrationNormalizer(t *testing.T, owner *cooperative.Owner, session func() json.RawMessage) *sdkoutput.Normalizer {
	t.Helper()
	factory, err := messages.NewFactory(func() string { return "original-uuid" }, func() time.Time { return time.UnixMilli(1700000000000) })
	if err != nil {
		t.Fatal(err)
	}
	n, err := sdkoutput.New(sdkoutput.Dependencies{Factory: factory, SessionID: session, NowMillis: func() float64 { return 1700000000000 }, OriginalEnv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func TestConsumerNormalizerAwaitsResultReaction(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	observed := false
	events := []string{}
	n := administrationNormalizer(t, owner, func() json.RawMessage {
		events = append(events, "original-session-getter")
		_, _ = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
			_, err := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
				observed = true
				events = append(events, "competing-reaction")
				return nil
			})
			return err
		})
		return json.RawMessage(`"original-session"`)
	})
	h := &Host{options: Options{Owner: owner, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error {
		events = append(events, "consumer")
		if !observed {
			return errors.New("normalizer consumer bypassed result Promise await")
		}
		return nil
	}}}, normalizer: n}
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"original prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	failure := owner.Run(context.Background(), func(scope *cooperative.Scope) error { _, err := h.normalizeYields(scope, message); return err })
	var observedEvents []string
	if err := owner.Run(context.Background(), func(*cooperative.Scope) error { observedEvents = append([]string(nil), events...); return nil }); err != nil {
		t.Fatal(err)
	}
	if failure != nil {
		t.Fatalf("%v; events=%v", failure, observedEvents)
	}
}
func TestConsumerNormalizerReturnAwaitsResultReaction(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	returned := false
	n := administrationNormalizer(t, owner, func() json.RawMessage { return json.RawMessage(`"original-session"`) })
	h := &Host{options: Options{Owner: owner, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error {
		_, _ = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
			_, err := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { returned = true; return nil })
			return err
		})
		return nil
	}, Receipt: func([]byte) *cooperative.Yield {
		receipt := cooperative.NewYield()
		receipt.Resolve(cooperative.Stop, nil)
		return receipt
	}}}, normalizer: n}
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"original prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		action, err := h.normalizeYields(scope, message)
		if err != nil {
			return err
		}
		if action != requestretry.Stop {
			return errors.New("normal stop changed")
		}
		if !returned {
			return errors.New("normalizer return bypassed result Promise await")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestConsumerQueryCancelledScopeSettlesHeldObligationsBeforeOwnerClose(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("original administrative scope cause")
	var bridge *queryIteratorBridge
	checkpointCalls := 0
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		bridge, err = newQueryIteratorBridge(scope, owner)
		if err != nil {
			return err
		}
		cancel(cause)
		return bridge.finish(scope, queryloop.Outcome{}, cause, func() error { checkpointCalls++; return errors.New("invalid Scope cannot execute business checkpoint") })
	})
	if !errors.Is(err, cause) {
		t.Fatalf("cause changed: %v", err)
	}
	// Owner stays alive. Cleanup must run in its actual FIFO; Close is not a fix.
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for name, future := range map[string]*OwnerFuture[struct{}]{"checkpointStarted": bridge.checkpointStarted, "checkpointResult": bridge.checkpointResult} {
			if !settled(future.Done()) {
				return fmt.Errorf("%s retained until Owner.Close", name)
			}
			if _, failure := future.Wait(context.Background()); !errors.Is(failure, cause) {
				return fmt.Errorf("%s rejection cause changed: %v", name, failure)
			}
		}
		if !settled(bridge.next.Done()) {
			return errors.New("query consumer future retained until Owner.Close")
		}
		if _, failure := bridge.next.Wait(context.Background()); !errors.Is(failure, cause) {
			return fmt.Errorf("query consumer rejection cause changed: %v", failure)
		}
		if checkpointCalls != 0 {
			return errors.New("administrative cleanup executed a business checkpoint")
		}
		for _, iterator := range []*OwnerIterator[queryValue]{bridge.loop, bridge.query, bridge.history} {
			future, err := iterator.Next(scope)
			if err != nil {
				return err
			}
			result, err := future.Await(scope)
			if err != nil || !result.Done || result.Defined {
				return errors.New("cancelled query iterator remained observable")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// This is administrative host cancellation while an output receipt is pending,
// not an SDK model AbortController and not a physical EPIPE assertion.
func TestConsumerNormalizerCancelledReceiptBodyIsDistinctFromModelAbort(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("output observer admission cancelled")
	receipt := cooperative.NewYield()
	n := administrationNormalizer(t, owner, func() json.RawMessage { return json.RawMessage(`"session"`) })
	h := &Host{options: Options{Owner: owner, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error {
		_, err := owner.Microtask(cooperative.Cancel, func(*cooperative.Scope) error { cancel(cause); return nil })
		return err
	}, Receipt: func([]byte) *cooperative.Yield { return receipt }}}, normalizer: n}
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	failure := owner.Run(ctx, func(scope *cooperative.Scope) error { _, err := h.normalizeYields(scope, message); return err })
	if failure != cause || IsConsumerAdministrativeFailure(failure) || requestretry.IsCallerAbort(failure) {
		t.Fatalf("classification %T %v", failure, failure)
	}
	if err := owner.Run(context.Background(), func(*cooperative.Scope) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerNormalizerOutputBodyThrowRetainsIdentity(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	original := errors.New("original output body throw")
	getters := 0
	n := administrationNormalizer(t, owner, func() json.RawMessage { getters++; return json.RawMessage(`"session"`) })
	h := &Host{options: Options{Owner: owner, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error { return original }}}, normalizer: n}
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := h.normalizeYields(scope, message)
		if err != original || IsConsumerAdministrativeFailure(err) || getters != 1 {
			return fmt.Errorf("body error/getters: %v %d", err, getters)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerSyncNormalizerRejectsForeignScopeBeforeAdvancing(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	getters := 0
	n := administrationNormalizer(t, owner, func() json.RawMessage { getters++; return json.RawMessage(`"session"`) })
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	producer := &syncNormalizer{owner: owner, producer: n.IterateMessage(message)}
	if producer.SyncIteratorOwner() != owner {
		t.Fatal("owner identity")
	}
	if err := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := producer.NextSync(scope, SyncNextRequest{}); err != cooperative.ErrRestricted {
			return fmt.Errorf("foreign Next: %v", err)
		}
		if _, err := producer.ReturnSync(scope, SyncReturnRequest[*sdkoutput.View]{}); err != cooperative.ErrRestricted {
			return fmt.Errorf("foreign Return: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		result, err := producer.NextSync(scope, SyncNextRequest{})
		if err != nil || result.Done || getters != 1 {
			return fmt.Errorf("foreign scope advanced producer: %v done=%v getters=%d", err, result.Done, getters)
		}
		result, err = producer.ReturnSync(scope, SyncReturnRequest[*sdkoutput.View]{ArgumentPresent: true, Defined: true, Value: result.Value})
		if err != nil || !result.Done || !result.Defined || result.Value == nil {
			return fmt.Errorf("original Return value changed: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerQueryForeignScopeDoesNotCreateDemand(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	if _, err := newQueryIteratorBridge(nil, owner); err != cooperative.ErrRestricted {
		t.Fatalf("nil Scope %v", err)
	}
	var bridge *queryIteratorBridge
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		bridge, err = newQueryIteratorBridge(scope, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := newQueryIteratorBridge(scope, owner); err != cooperative.ErrRestricted {
			return fmt.Errorf("foreign construction %v", err)
		}
		if _, err := bridge.emit(scope, queryloop.Yield{}, func(queryloop.Yield) (requestretry.NoticeAction, error) {
			return 0, errors.New("foreign consumer executed")
		}); err != cooperative.ErrRestricted {
			return fmt.Errorf("foreign emit %v", err)
		}
		if err := bridge.finish(scope, queryloop.Outcome{}, nil, func() error { return errors.New("foreign checkpoint executed") }); err != cooperative.ErrRestricted {
			return fmt.Errorf("foreign finish %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if bridge.resume != nil {
			return errors.New("foreign Scope wrote demand")
		}
		return bridge.finish(scope, queryloop.Outcome{}, nil, func() error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerAdministrativeClassificationPreservesOwnerFailure(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("original scope cause")
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel(cause)
		_, native := provider.NewOwnerNativeIO(queryFailedBoundary{scope: scope}, nil)
		if consumerAdministration(native) != native {
			return errors.New("typed owner failure replaced")
		}
		local := consumerAdministration(cause)
		if consumerAdministration(local) != local || local.Error() != "query consumer administration: "+cause.Error() || !errors.Is(local, cause) {
			return fmt.Errorf("local cause identity %v", local)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerAbandonedNormalizerIsClosedOnCanonicalOwner(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	n := administrationNormalizer(t, owner, func() json.RawMessage { return json.RawMessage(`"session"`) })
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	producer := &syncNormalizer{owner: owner, producer: n.IterateMessage(message)}
	future, _ := NewOwnerFuture[IteratorResult[*sdkoutput.View]](owner)
	cause := errors.New("original cancelled observer")
	h := &Host{options: Options{Owner: owner}}
	if err := owner.Run(context.Background(), func(*cooperative.Scope) error { h.abandonNormalizer(producer, future, cause); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if !settled(future.Done()) {
			return errors.New("adapter obligation leaked")
		}
		if _, err := future.Wait(context.Background()); err != cause {
			return fmt.Errorf("adapter rejection identity %v", err)
		}
		result, err := producer.NextSync(scope, SyncNextRequest{})
		if err != nil || !result.Done {
			return fmt.Errorf("suspended normalizer remained active %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Closing the actual Owner admits no late business callback. It may only reject
// immutable obligations; this is separate from cancellation while Owner is alive.
func TestConsumerOwnerClosingRejectsObligationsWithoutWorkerBusiness(t *testing.T) {
	owner := cooperative.New()
	entered := make(chan struct{})
	finished := make(chan error, 1)
	var bridge *queryIteratorBridge
	var normalizerReceipt *OwnerFuture[IteratorResult[*sdkoutput.View]]
	n := administrationNormalizer(t, owner, func() json.RawMessage { return json.RawMessage(`"session"`) })
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	producer := &syncNormalizer{owner: owner, producer: n.IterateMessage(message)}
	h := &Host{options: Options{Owner: owner}}
	go func() {
		finished <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var err error
			bridge, err = newQueryIteratorBridge(scope, owner)
			if err != nil {
				return err
			}
			normalizerReceipt, _ = NewOwnerFuture[IteratorResult[*sdkoutput.View]](owner)
			close(entered)
			err = scope.AwaitPromise(bridge.checkpointStarted.Done())
			h.abandonNormalizer(producer, normalizerReceipt, err)
			return bridge.finish(scope, queryloop.Outcome{}, err, func() error { return errors.New("closing owner ran business checkpoint") })
		})
	}()
	<-entered
	owner.Close()
	if err := <-finished; !errors.Is(err, cooperative.ErrClosed) {
		t.Fatalf("owner closing identity %v", err)
	}
	for _, future := range []*OwnerFuture[struct{}]{bridge.checkpointStarted, bridge.checkpointResult} {
		if !settled(future.Done()) {
			t.Fatal("closing owner obligation")
		}
	}
	if !settled(bridge.next.Done()) || !settled(normalizerReceipt.Done()) {
		t.Fatal("closing owner pending immutable observer")
	}
}

func TestConsumerNormalizerKeepsOriginalProviderOwnerFailure(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("same physical owner cause")
	var scope *cooperative.Scope
	var original error
	n := administrationNormalizer(t, owner, func() json.RawMessage { return json.RawMessage(`"session"`) })
	h := &Host{options: Options{Owner: owner, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error {
		cancel(cause)
		_, original = provider.NewOwnerNativeIO(queryFailedBoundary{scope: scope}, nil)
		return original
	}}}, normalizer: n}
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := owner.Run(ctx, func(next *cooperative.Scope) error {
		scope = next
		_, err := h.normalizeYields(scope, message)
		return err
	})
	if got != original || !provider.IsOwnerWaitFailure(got) || IsConsumerAdministrativeFailure(got) || !errors.Is(got, cause) {
		t.Fatalf("original administrative type replaced: %T %v original=%T", got, got, original)
	}
}

// A body throw keeps its original identity even when it happens to equal the
// host cancellation cause. This is AsyncIteratorClose precedence, not a new
// SDK model abort classification or a claimed OS stdio error.
func TestConsumerNormalizerBodyCauseIsNotReclassified(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	body := errors.New("original body and cancellation cause")
	n := administrationNormalizer(t, owner, func() json.RawMessage { return json.RawMessage(`"session"`) })
	h := &Host{options: Options{Owner: owner, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error { cancel(body); return body }}}, normalizer: n}
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := owner.Run(ctx, func(scope *cooperative.Scope) error { _, err := h.normalizeYields(scope, message); return err })
	if got != body || IsConsumerAdministrativeFailure(got) {
		t.Fatalf("body exception identity changed: got=%T %v want=%v", got, got, body)
	}
}

func TestConsumerNormalizerCancelledAdapterHasAdministrativeType(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("adapter observer cancelled before Next")
	n := administrationNormalizer(t, owner, func() json.RawMessage { return json.RawMessage(`"session"`) })
	h := &Host{options: Options{Owner: owner, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error { return errors.New("cancelled adapter emitted output") }}}, normalizer: n}
	message, err := sdkoutput.Decode(json.RawMessage(`{"type":"user","uuid":"original-user","message":{"role":"user","content":"prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel(cause)
		_, err := h.normalizeYields(scope, message)
		return err
	})
	if !IsConsumerAdministrativeFailure(got) || !errors.Is(got, cause) || requestretry.IsCallerAbort(got) {
		t.Fatalf("adapter administration %T %v", got, got)
	}
}
