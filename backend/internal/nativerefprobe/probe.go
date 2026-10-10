//go:build cch_native_ref_probe

// Package nativerefprobe is a fixture-only observer of original native helpers.
// It is not Source policy, a production Owner loop or a ledger implementation.
package nativerefprobe

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"testing"
)

type Observation struct {
	PID                          int      `json:"pid"`
	Path                         string   `json:"path"`
	Kind                         string   `json:"kind"`
	Mode                         string   `json:"mode"`
	CountBefore                  int      `json:"countBefore"`
	CountAdmitted                int      `json:"countAdmitted"`
	CountConsumer                int      `json:"countConsumer"`
	CountAfter                   int      `json:"countAfter"`
	IdleClosed                   bool     `json:"idleClosed"`
	QueueCount                   int      `json:"queueCount"`
	FutureIdentity               string   `json:"futureIdentity"`
	FutureIdentityAfter          string   `json:"futureIdentityAfter"`
	ConsumerFutureIdentity       string   `json:"consumerFutureIdentity"`
	OriginalFutureUnchanged      bool     `json:"originalFutureUnchanged"`
	ReleaseReceiptIdentity       string   `json:"releaseReceiptIdentity"`
	ReleaseReceiptError          string   `json:"releaseReceiptError"`
	ConsumerReceiptError         string   `json:"consumerReceiptError"`
	OriginalFutureError          string   `json:"originalFutureError"`
	OriginalFutureDoneAtConsumer bool     `json:"originalFutureDoneAtConsumer"`
	PhysicalBytesSHA             string   `json:"physicalBytesSHA"`
	PhysicalComplete             bool     `json:"physicalComplete"`
	OwnerClosed                  bool     `json:"ownerClosed"`
	Timeline                     []string `json:"timeline"`
}

func errText(e error) string {
	if e != nil {
		return e.Error()
	}
	return ""
}

// Observe runs no pending native I/O while closing: begin's physical operation
// is already complete before its original completion resolves the future and
// queues the release. The hold is only inside the actual registered Then.
func Observe[T any](t *testing.T, owner *cooperative.Owner, refs *ownerasync.NativeReferences, closeAtConsumer bool, begin func(*cooperative.Scope) (*ownerasync.OwnerFuture[T], error)) (Observation, T, T) {
	t.Helper()
	observed := Observation{Mode: "normal-release"}
	if closeAtConsumer {
		observed.Mode = "close-at-consumer"
	}
	observed.CountBefore, _ = refs.Snapshot()
	consumerEntered, consumerRelease := make(chan struct{}), make(chan struct{})
	var parent *ownerasync.OwnerFuture[T]
	var child *ownerasync.OwnerFuture[struct{}]
	var original T
	var originalFailure error
	var releaseReceipt *cooperative.Receipt
	runErr := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		parent, e = begin(scope)
		if e != nil {
			return e
		}
		observed.CountAdmitted, _ = refs.Snapshot()
		observed.FutureIdentity = fmt.Sprintf("%p", parent)
		child, e = parent.Then(scope, func(_ *cooperative.Scope, value T, failure error) error {
			original, originalFailure = value, failure
			observed.CountConsumer, _ = refs.Snapshot()
			observed.QueueCount = len(owner.ProbeMicrotaskReceipts())
			queue := owner.ProbeMicrotaskReceipts()
			if len(queue) == 1 {
				releaseReceipt = queue[0]
				observed.ReleaseReceiptIdentity = fmt.Sprintf("%p", releaseReceipt)
			}
			select {
			case <-parent.Done():
				observed.OriginalFutureDoneAtConsumer = true
			default:
			}
			close(consumerEntered)
			<-consumerRelease
			return nil
		})
		return e
	})
	if runErr != nil {
		owner.Close()
		t.Fatal("original owner admission", runErr)
	}
	<-consumerEntered
	observed.ConsumerFutureIdentity = fmt.Sprintf("%p", child)
	observed.Timeline = []string{"native-admitted", "actual-private-FS-complete", "original-future-resolved", "consumer-reaction-entered", "original-release-receipt-queued"}
	if closeAtConsumer {
		closed := make(chan struct{})
		go func() { owner.Close(); close(closed) }()
		<-owner.ProbeLifetimeDone()
		observed.Timeline = append(observed.Timeline, "original-owner-lifetime-canceled")
		close(consumerRelease)
		<-closed
		observed.OwnerClosed = true
	} else {
		close(consumerRelease)
	}
	// All waits below observe already-established obligations; no ledger idle
	// wait or elapsed-time detection is used to report a leaked reference.
	_, childErr := child.Wait(context.Background())
	observed.ConsumerReceiptError = errText(childErr)
	value, parentErr := parent.Wait(context.Background())
	observed.OriginalFutureError = errText(parentErr)
	observed.FutureIdentityAfter = fmt.Sprintf("%p", parent)
	observed.OriginalFutureUnchanged = parentErr == originalFailure && observed.FutureIdentityAfter == observed.FutureIdentity
	if releaseReceipt != nil {
		observed.ReleaseReceiptError = errText(releaseReceipt.Wait())
	}
	observed.CountAfter, _ = refs.Snapshot()
	_, reclaimed := refs.Snapshot()
	<-reclaimed
	observed.CountAfter, _ = refs.Snapshot()
	_, idle := refs.Snapshot()
	select {
	case <-idle:
		observed.IdleClosed = true
	default:
	}
	if !closeAtConsumer {
		owner.Close()
		observed.OwnerClosed = true
	}
	observed.Timeline = append(observed.Timeline, "release-receipt-observed", "actual-ledger-idle-receipt-settled", "owner-close-returned")
	if observed.QueueCount != 1 || !observed.OriginalFutureDoneAtConsumer || !observed.OriginalFutureUnchanged {
		t.Fatalf("required original cutpoint not established: %+v", observed)
	}
	return observed, value, original
}
