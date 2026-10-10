package cooperative

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPromiseAwaitResumesAtOwnFIFOReaction(t *testing.T) {
	owner := New()
	defer owner.Close()
	var order []string
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		_, err := owner.Microtask(Read, func(*Scope) error {
			order = append(order, "before")
			_, err := owner.Microtask(Read, func(*Scope) error { order = append(order, "nested"); return nil })
			return err
		})
		if err != nil {
			return err
		}
		if err := scope.AwaitFulfilled(); err != nil {
			return err
		}
		order = append(order, "await-continuation")
		if !reflect.DeepEqual(order, []string{"before", "await-continuation"}) {
			return errors.New("nested reaction overtook await continuation")
		}
		if err := scope.AwaitFulfilled(); err != nil {
			return err
		}
		if !reflect.DeepEqual(order, []string{"before", "await-continuation", "nested"}) {
			return errors.New("nested reaction was lost")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPromiseFulfilledAwaitPrecedesReadyMacroCallback(t *testing.T) {
	owner := New()
	defer owner.Close()
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		macro := false
		if _, err := owner.Submit(context.Background(), Read, func(*Scope) error { macro = true; return nil }); err != nil {
			return err
		}
		if err := scope.AwaitFulfilled(); err != nil {
			return err
		}
		if macro {
			return errors.New("ready I/O callback overtook fulfilled Promise")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPromiseFulfilledCatchForwardsBeforeAwaitReaction(t *testing.T) {
	owner := New()
	defer owner.Close()
	var order []string
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		if _, err := owner.Microtask(Read, func(*Scope) error {
			order = append(order, "before")
			_, err := owner.Microtask(Read, func(*Scope) error { order = append(order, "nested"); return nil })
			return err
		}); err != nil {
			return err
		}
		if err := scope.AwaitFulfilledCatch(); err != nil {
			return err
		}
		order = append(order, "await-continuation")
		if !reflect.DeepEqual(order, []string{"before", "nested", "await-continuation"}) {
			return errors.New("catch-forwarding Promise FIFO changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
