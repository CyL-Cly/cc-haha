package nativemodule

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"runtime"
	"sync"
	"testing"
	"time"
)

type ownerInitializedNamespace struct {
	value    string
	exported func() string
}

func initializedValidator(owner *cooperative.Owner, namespace *ownerInitializedNamespace) func(*cooperative.Scope, string, *ownerInitializedNamespace) error {
	return func(scope *cooperative.Scope, key string, got *ownerInitializedNamespace) error {
		if !scope.BelongsTo(owner) || key != "compiled-module" || got != namespace {
			return cooperative.ErrRestricted
		}
		return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
	}
}
func TestOwnerInitializedCompiledModuleDefersRealExportsAndSharesOneInitialization(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	namespace := &ownerInitializedNamespace{}
	linked, evaluated := 0, 0
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, err := NewOwnerInitialized(owner, scope, "compiled-module", namespace, initializedValidator(owner, namespace), func(next *cooperative.Scope) error {
			linked++
			if !next.BelongsTo(owner) {
				return cooperative.ErrRestricted
			}
			if err := next.Frame(cooperative.FrameFeed, func() error { t.Error("initializer acquired a provider Frame"); return nil }); err != cooperative.ErrRestricted {
				t.Error("initializer callback lost its restriction", err)
			}
			namespace.exported = func() string { return namespace.value }
			return nil
		}, func(*cooperative.Scope) error { evaluated++; namespace.value = "actual evaluated export"; return nil })
		if err != nil {
			return err
		}
		select {
		case <-entry.fetch.Done():
		default:
			return errors.New("already compiled code was mislabeled pending fetch")
		}
		select {
		case <-entry.load.Done():
			return errors.New("module linked before its real Owner job")
		default:
		}
		select {
		case <-entry.evaluation.Done():
			return errors.New("module evaluated before its real Owner job")
		default:
		}
		one, err := entry.Import(scope)
		if err != nil {
			return err
		}
		two, err := entry.Import(scope)
		if err != nil {
			return err
		}
		if one == two || one.Identity() == two.Identity() {
			return errors.New("concurrent consumers shared a public Promise")
		}
		if linked != 0 || evaluated != 0 || namespace.exported != nil {
			return errors.New("constructor ran real initialization inline")
		}
		first, err := one.Await(scope)
		if err != nil {
			return err
		}
		second, err := two.Await(scope)
		if err != nil {
			return err
		}
		if first != namespace || second != namespace || namespace.exported() == "" || linked != 1 || evaluated != 1 {
			return fmt.Errorf("real module work or shared namespace lost: linked=%d evaluated=%d", linked, evaluated)
		}
		warm, err := entry.Import(scope)
		if err != nil {
			return err
		}
		third, err := warm.Await(scope)
		if err != nil {
			return err
		}
		if warm == one || third != namespace || linked != 1 || evaluated != 1 {
			return errors.New("warm import reran initialization or reused public identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestOwnerInitializedModuleCachesExactLinkAndEvaluationErrors(t *testing.T) {
	for _, stage := range []string{"link", "evaluation"} {
		t.Run(stage, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			namespace := &ownerInitializedNamespace{}
			original := errors.New("actual module constructor failure")
			links, evaluations := 0, 0
			if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
				entry, err := NewOwnerInitialized(owner, scope, "compiled-module", namespace, initializedValidator(owner, namespace), func(*cooperative.Scope) error {
					links++
					if stage == "link" {
						return original
					}
					namespace.exported = func() string { return namespace.value }
					return nil
				}, func(*cooperative.Scope) error { evaluations++; return original })
				if err != nil {
					return err
				}
				for range 2 {
					pending, err := entry.Import(scope)
					if err != nil {
						return err
					}
					if _, err = pending.Await(scope); err != original {
						return fmt.Errorf("constructor error identity replaced or pending: %v", err)
					}
				}
				wantEval := 1
				if stage == "link" {
					wantEval = 0
				}
				if links != 1 || evaluations != wantEval {
					return fmt.Errorf("failed module retried: links=%d evaluations=%d", links, evaluations)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOwnerInitializedModuleCallerCancellationKeepsAdmittedInitializationAlive(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	namespace := &ownerInitializedNamespace{}
	linked := 0
	var pending interface {
		Wait(context.Context) (*ownerInitializedNamespace, error)
	}
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, err := NewOwnerInitialized(owner, scope, "compiled-module", namespace, initializedValidator(owner, namespace), func(*cooperative.Scope) error {
			linked++
			namespace.exported = func() string { return namespace.value }
			return nil
		}, func(*cooperative.Scope) error { namespace.value = "independent result"; return nil })
		if err != nil {
			return err
		}
		result, err := entry.Import(scope)
		if err != nil {
			return err
		}
		pending = result
		cancel()
		if _, err = result.Await(scope); !errors.Is(err, context.Canceled) {
			return fmt.Errorf("caller wait did not cancel: %v", err)
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wait, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWait()
	value, err := pending.Wait(wait)
	if err != nil || value != namespace || linked != 1 || namespace.exported() != "independent result" {
		t.Fatal("one cancelled observer poisoned real initialization", value, err, linked)
	}
}
func TestOwnerInitializedModuleCloseBeforeTaskDoesNotConstructOrCallExports(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	namespace := &ownerInitializedNamespace{}
	linked, evaluated := 0, 0
	var pending interface {
		Wait(context.Context) (*ownerInitializedNamespace, error)
	}
	startClose, closed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	requestClose := func() { once.Do(func() { close(startClose) }) }
	defer requestClose()
	go func() { <-startClose; owner.Close(); close(closed) }()
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, err := NewOwnerInitialized(owner, scope, "compiled-module", namespace, initializedValidator(owner, namespace), func(*cooperative.Scope) error {
			linked++
			namespace.exported = func() string { return "late" }
			return nil
		}, func(*cooperative.Scope) error { evaluated++; return nil })
		if err != nil {
			return err
		}
		result, err := entry.Import(scope)
		if err != nil {
			return err
		}
		pending = result
		requestClose()
		for {
			if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
				return err
			}
			runtime.Gosched()
		}
	})
	if !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("global Close failed to cancel held original stack", err)
	}
	<-closed
	if _, err = pending.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("Close left import pending/successful", err)
	}
	if linked != 0 || evaluated != 0 || namespace.exported != nil {
		t.Fatal("queued initializer executed after Close", linked, evaluated)
	}
}

func TestOwnerInitializedModuleRejectsInvalidConstructorsWithoutInitialization(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	namespace := &ownerInitializedNamespace{}
	calls := 0
	link := func(*cooperative.Scope) error {
		calls++
		namespace.exported = func() string { return namespace.value }
		return nil
	}
	evaluate := func(*cooperative.Scope) error { namespace.value = "ready"; return nil }
	validate := initializedValidator(owner, namespace)
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := NewOwnerInitialized(owner, scope, "compiled-module", namespace, validate, link, evaluate); err != cooperative.ErrRestricted {
			return fmt.Errorf("foreign constructor admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var expired *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		expired = scope
		cases := []struct {
			owner          *cooperative.Owner
			scope          *cooperative.Scope
			namespace      *ownerInitializedNamespace
			validate       func(*cooperative.Scope, string, *ownerInitializedNamespace) error
			link, evaluate func(*cooperative.Scope) error
		}{
			{nil, scope, namespace, validate, link, evaluate}, {owner, nil, namespace, validate, link, evaluate}, {owner, scope, nil, validate, link, evaluate}, {owner, scope, namespace, nil, link, evaluate}, {owner, scope, namespace, validate, nil, evaluate}, {owner, scope, namespace, validate, link, nil},
		}
		for _, c := range cases {
			if _, err := NewOwnerInitialized(c.owner, c.scope, "compiled-module", c.namespace, c.validate, c.link, c.evaluate); err != cooperative.ErrRestricted {
				return fmt.Errorf("invalid constructor admitted: %v", err)
			}
		}
		if calls != 0 {
			return errors.New("invalid constructor executed module body")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewOwnerInitialized(owner, expired, "compiled-module", namespace, validate, link, evaluate); err != cooperative.ErrRestricted {
		t.Fatal("expired constructor admitted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		cancel()
		_, err := NewOwnerInitialized(owner, scope, "compiled-module", namespace, validate, link, evaluate)
		if !errors.Is(err, context.Canceled) {
			return fmt.Errorf("cancelled constructor admitted: %v", err)
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("unadmitted constructor executed real work", calls)
	}
}
