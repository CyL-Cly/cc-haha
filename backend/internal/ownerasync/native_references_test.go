package ownerasync

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

func TestNativeBindingIdentityAndIdempotentDelivery(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	var refs NativeReferences
	b, e := refs.Bind(owner)
	if e != nil {
		t.Fatal(e)
	}
	again, e := refs.Bind(owner)
	if e != nil || again != b {
		t.Fatal("second counter", e)
	}
	if _, e = refs.Bind(other); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if (&NativeBinding{}).Owns(owner) || b.Same(&NativeBinding{}) || b.Same(nil) || !b.Same(b) {
		t.Fatal("fabricated native binding")
	}
	if _, e = (*NativeReferences)(nil).Bind(owner); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e = (&NativeReferences{}).Bind(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e = b.Acquire(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e = other.Run(context.Background(), func(s *cooperative.Scope) error { _, e := b.Acquire(s); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	var first, second *NativeReference
	if e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
		var e error
		first, e = b.Acquire(s)
		if e != nil {
			return e
		}
		second, e = b.Acquire(s)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	n, idle := refs.Snapshot()
	if n != 2 {
		t.Fatal(n)
	}
	first.DeliveryFinished()
	first.DeliveryFinished()
	select {
	case <-idle:
		t.Fatal("one job released another reference")
	default:
	}
	second.DeliveryFinished()
	second.DeliveryFinished()
	select {
	case <-idle:
	default:
		t.Fatal("actual final delivery absent")
	}
	if n, _ = refs.Snapshot(); n != 0 {
		t.Fatal(n)
	}
	(*NativeReference)(nil).DeliveryFinished()
	(&NativeReference{}).DeliveryFinished()
}
