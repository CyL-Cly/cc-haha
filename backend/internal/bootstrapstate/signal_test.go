package bootstrapstate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func run(t *testing.T, owner *cooperative.Owner, fn func(*cooperative.Scope) error) {
	t.Helper()
	if err := owner.Run(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}
func TestSignalLiveSet(t *testing.T) {
	for _, mutation := range []string{"delete-future", "add", "remove-readd-future", "remove-readd-current", "clear", "clear-add", "nested", "throw"} {
		t.Run(mutation, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			signal, _ := NewSignal[string](owner)
			trace := []string{}
			first := true
			original := errors.New("original")
			var a, b, c *Listener[string]
			var removeA, removeB Unsubscribe
			b = NewListener(func(_ *cooperative.Scope, value string) error { trace = append(trace, "b:"+value); return nil })
			c = NewListener(func(_ *cooperative.Scope, value string) error { trace = append(trace, "c:"+value); return nil })
			a = NewListener(func(scope *cooperative.Scope, value string) error {
				trace = append(trace, "a:"+value)
				if !first {
					return nil
				}
				first = false
				switch mutation {
				case "delete-future":
					return removeB(scope)
				case "add":
					_, err := signal.Subscribe(scope, c)
					return err
				case "remove-readd-future":
					if err := removeB(scope); err != nil {
						return err
					}
					_, err := signal.Subscribe(scope, b)
					return err
				case "remove-readd-current":
					if err := removeA(scope); err != nil {
						return err
					}
					_, err := signal.Subscribe(scope, a)
					return err
				case "clear":
					return signal.Clear(scope)
				case "clear-add":
					if err := signal.Clear(scope); err != nil {
						return err
					}
					_, err := signal.Subscribe(scope, c)
					return err
				case "nested":
					return signal.Emit(scope, "inner")
				case "throw":
					return original
				}
				return nil
			})
			run(t, owner, func(scope *cooperative.Scope) error {
				removeA, _ = signal.Subscribe(scope, a)
				removeB, _ = signal.Subscribe(scope, b)
				if mutation == "remove-readd-future" {
					_, _ = signal.Subscribe(scope, c)
				}
				err := signal.Emit(scope, "outer")
				if mutation == "throw" {
					if err != original {
						t.Fatal("throw identity lost", err)
					}
					return nil
				}
				return err
			})
			want := map[string][]string{
				"delete-future": {"a:outer"}, "add": {"a:outer", "b:outer", "c:outer"},
				"remove-readd-future": {"a:outer", "c:outer", "b:outer"}, "remove-readd-current": {"a:outer", "b:outer", "a:outer"},
				"clear": {"a:outer"}, "clear-add": {"a:outer", "c:outer"}, "nested": {"a:outer", "a:inner", "b:inner", "b:outer"}, "throw": {"a:outer"},
			}[mutation]
			if !reflect.DeepEqual(trace, want) {
				t.Fatalf("live iteration changed: got %v want %v", trace, want)
			}
		})
	}
}
func TestSignalIdentityAndOldUnsubscribe(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	signal, _ := NewSignal[string](owner)
	count := 0
	listener := NewListener(func(*cooperative.Scope, string) error { count++; return nil })
	run(t, owner, func(scope *cooperative.Scope) error {
		one, _ := signal.Subscribe(scope, listener)
		two, _ := signal.Subscribe(scope, listener)
		_ = signal.Emit(scope, "one")
		_ = one(scope)
		_ = signal.Emit(scope, "two")
		_, _ = signal.Subscribe(scope, listener)
		_ = two(scope)
		_ = signal.Emit(scope, "three")
		if count != 1 {
			t.Fatal("subscription token replaced function identity", count)
		}
		return nil
	})
}
func TestSignalRejectsForeignOrInvalidScopeBeforeEffects(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	if _, err := NewSignal[string](nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	signal, _ := NewSignal[string](owner)
	run(t, foreign, func(scope *cooperative.Scope) error {
		if _, err := signal.Subscribe(scope, NewListener(func(*cooperative.Scope, string) error { t.Fatal("foreign listener entered"); return nil })); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		if err := signal.Emit(scope, "bad"); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		if err := signal.Clear(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		return nil
	})
	run(t, owner, func(scope *cooperative.Scope) error {
		if _, err := signal.Subscribe(scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		if _, err := signal.Subscribe(scope, NewListener[string](nil)); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
		return nil
	})
}
