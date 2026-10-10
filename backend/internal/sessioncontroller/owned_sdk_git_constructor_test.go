package sessioncontroller

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestOwnedSDKGitConstructionNeverCreatesImplicitFallback(t *testing.T) {
	c, state, owner := boundController(t)
	options := c.options
	options.RecordBranchProfile = OwnedGitRecordProfile
	binding, e := NewOwnedGitRecordBranch(state, actualOwnedGitService(t, state, owner))
	if e != nil {
		t.Fatal(e)
	}
	options.BeginOwnedRecordBranch = binding
	options.AsyncPersistence = true
	if _, e := New(options); e == nil {
		t.Fatal("explicit synchronous fallback admitted")
	}
	options.WriterOptions.Branch = nil
	next, e := New(options)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = next.writer.options.Branch(); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("bound sync consumer returned fake branch", e)
	}
	if e = owner.Run(context.Background(), func(s *cooperative.Scope) error {
		_, e := next.writer.Record(nil, nil)
		if !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("bound sync Record admitted")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Options){func(o *Options) { o.AsyncPersistence = false }, func(o *Options) { o.BeginOwnedRecordBranch = nil }, func(o *Options) { o.Bootstrap = nil }} {
		bad := options
		mutate(&bad)
		if _, e := New(bad); e == nil {
			t.Fatal("unavailable owner branch admitted")
		}
	}
}
