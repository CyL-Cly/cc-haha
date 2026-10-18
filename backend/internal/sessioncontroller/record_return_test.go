package sessioncontroller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestRecordReturnAdoptionForcedOwnerCloseRejectsOnlyObserverObligation(t *testing.T) {
	owner := cooperative.New()
	pending := &PendingWriter{owner: owner}
	job := &recordContinuation{pending: pending, future: &RecordFuture{owner: owner, future: newWriteFuture()}}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		job.token, err = pending.BeginTrackedWrite(scope)
		if err != nil {
			return err
		}
		if err := job.beginInsertReturn(scope); err != nil {
			return err
		}
		job.adoptInsertReturn(scope)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := job.future.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("Owner close stranded or resolved the actual Record obligation", err)
	}
	if !job.token.active || pending.tracked != 1 {
		t.Fatal("administrative close ran business finally off Owner", pending.tracked)
	}
}

func TestRecordReturnAdoptionRejectsForeignMissingAndDuplicateCapabilities(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	pending := &PendingWriter{owner: owner}
	job := &recordContinuation{pending: pending, future: &RecordFuture{owner: owner, future: newWriteFuture()}}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var err error
		job.token, err = pending.BeginTrackedWrite(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := job.beginInsertReturn(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil scope admitted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := job.beginInsertReturn(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign Record scope admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, invalid := range []*recordContinuation{
			nil,
			{},
			{pending: pending, token: job.token},
			{pending: pending, token: job.token, future: &RecordFuture{owner: foreign, future: newWriteFuture()}},
			{pending: pending, token: &TrackedWrite{pending: &PendingWriter{owner: owner}}, future: job.future},
		} {
			if err := invalid.beginInsertReturn(scope); !errors.Is(err, cooperative.ErrRestricted) {
				t.Error("missing/foreign original Record capability admitted", err)
			}
		}
		if job.insertBody != nil || pending.tracked != 1 {
			t.Error("rejected caller changed business state", pending.tracked)
		}
		if err := job.beginInsertReturn(scope); err != nil {
			return err
		}
		if err := job.beginInsertReturn(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("duplicate original insert Promise admitted", err)
		}
		job.adoptInsertReturn(scope)
		// The actual body rejects; its own finally still runs once on Owner.
		job.finish(scope, context.Canceled)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := job.future.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("insert rejection reason changed", err)
	}
	if err := owner.Run(context.Background(), func(*cooperative.Scope) error {
		if job.token != nil || pending.tracked != 0 {
			t.Error("rejected body's actual finally did not retire its own token", pending.tracked)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
