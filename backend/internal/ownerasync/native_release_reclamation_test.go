package ownerasync

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"testing"
)

func TestReclaimRejectedReceipt(t *testing.T) {
	for _, mode := range []string{"rejected", "fulfilled-preserves-release-position", "duplicate", "nil-receiver", "nil-receipt"} {
		t.Run(mode, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			var refs NativeReferences
			binding, e := refs.Bind(owner)
			if e != nil {
				t.Fatal(e)
			}
			var ref *NativeReference
			var receipt *cooperative.Receipt
			var done, duplicate <-chan struct{}
			originalFailure := errors.New("original actual receipt rejection")
			if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				var e error
				ref, e = binding.Acquire(scope)
				if e != nil {
					return e
				}
				receipt, e = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
					if mode == "rejected" || mode == "duplicate" {
						return originalFailure
					}
					return nil
				})
				if e != nil {
					return e
				}
				switch mode {
				case "nil-receiver":
					done = (*NativeReference)(nil).ReclaimRejectedReceipt(receipt)
				case "nil-receipt":
					done = ref.ReclaimRejectedReceipt(nil)
				default:
					done = ref.ReclaimRejectedReceipt(receipt)
				}
				if mode == "duplicate" {
					duplicate = ref.ReclaimRejectedReceipt(receipt)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			<-done
			if duplicate != nil {
				<-duplicate
			}
			failure := receipt.Wait()
			count, idle := refs.Snapshot()
			if mode == "rejected" || mode == "duplicate" {
				if failure != originalFailure || count != 0 {
					t.Errorf("actual rejected receipt not reclaimed once, original cause lost: count=%d failure=%v", count, failure)
				}
				select {
				case <-idle:
				default:
					t.Error("rejected actual release did not settle actual ledger")
				}
				ref.DeliveryFinished()
				ref.DeliveryFinished()
				if count, _ := refs.Snapshot(); count != 0 {
					t.Error("duplicate recovery released twice", count)
				}
			} else {
				// Successful receipt does not move the original callback's release point.
				// This isolated callback deliberately does not end the ref; the helper must
				// not manufacture a successful release. It is not a Source business mock.
				if failure != nil || count != 1 {
					t.Errorf("successful/nil observer changed release ownership: count=%d error=%v", count, failure)
				}
				select {
				case <-idle:
					t.Error("successful/nil observer closed outstanding obligation")
				default:
				}
				ref.DeliveryFinished()
			}
		})
	}
}
