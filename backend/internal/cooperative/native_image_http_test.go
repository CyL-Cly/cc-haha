package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestNativeImageHTTPPurposeSameOwnerCallbackForeignStaleAndGenericGuard(t *testing.T) {
	owner, foreign := New(), New()
	defer owner.Close()
	defer foreign.Close()
	var proof *NativeImageHTTP
	var stale *Scope
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		var err error
		proof, err = NewNativeImageHTTP(scope, owner)
		if err != nil {
			return err
		}
		if _, err := NewNativeImageHTTP(scope, foreign); !errors.Is(err, ErrRestricted) {
			t.Error("foreign image issuer", err)
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, err := NewNativeImageHTTP(callback, owner); !errors.Is(err, ErrRestricted) {
				t.Error("ordinary callback minted image purpose", err)
			}
			if _, err := callback.AwaitBytes(StreamNext, func(context.Context) ([]byte, error) { t.Error("generic body reader escaped"); return nil, nil }); !errors.Is(err, ErrRestricted) {
				t.Error("generic body reader guard", err)
			}
			return proof.Run(callback, context.Background(), "image_generate", func(producer *Scope) error {
				if producer == callback || !producer.BelongsTo(owner) {
					t.Error("image producer lost canonical owner")
				}
				data, err := producer.AwaitBytes(StreamNext, func(context.Context) ([]byte, error) { return []byte("original physical image"), nil })
				if err == nil && string(data) != "original physical image" {
					t.Error("immutable bytes changed")
				}
				return err
			})
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := proof.Run(stale, context.Background(), "image_generate", func(*Scope) error { t.Error("expired producer entered"); return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal("expired purpose", err)
	}
	if err := foreign.Run(context.Background(), func(scope *Scope) error {
		err := proof.Run(scope, context.Background(), "image_edit", func(*Scope) error { t.Error("foreign producer entered"); return nil })
		if !errors.Is(err, ErrRestricted) {
			t.Error("foreign image scope", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		for _, name := range []string{"sdk", "web_fetch_apply", "", "arbitrary"} {
			if err := proof.Run(scope, context.Background(), name, func(*Scope) error { t.Error("unsupported purpose entered"); return nil }); !errors.Is(err, ErrRestricted) {
				t.Error("image purpose classification", name, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
