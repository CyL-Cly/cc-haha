package requestretry

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeThinkingSummaryBindingRejectsForeignScopeAndPropagatesError(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	called := 0
	input := Input{Context: requestcontext.Context{Options: requestcontext.Options{Model: "actual-summary-model"}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile}}}
	expected := errors.New("actual settings read failed")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		deps := Dependencies{StreamOwner: owner, StreamScope: scope, NativeThinkingSummaryBeta: func(actual *cooperative.Scope, model string) (*bool, error) {
			called++
			if actual != scope || model != input.Context.Options.Model {
				t.Error("binding did not receive original current scope/model")
			}
			return nil, expected
		}}
		if err := BindNativeClaudeHost(context.Background(), &input, deps); !errors.Is(err, expected) {
			t.Error("actual settings error swallowed", err)
		}
		deps.StreamOwner = other
		if err := BindNativeClaudeHost(context.Background(), &input, deps); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign current scope admitted", err)
		}
		deps.StreamOwner = owner
		deps.NativeThinkingSummaryBeta = func(*cooperative.Scope, string) (*bool, error) { value := true; return &value, nil }
		if err := BindNativeClaudeHost(context.Background(), &input, deps); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if called != 1 || input.Context.Host.NativeThinkingSummaryBeta == nil || !*input.Context.Host.NativeThinkingSummaryBeta {
		t.Fatal("binding side effects/projection", called, input.Context.Host.NativeThinkingSummaryBeta)
	}
	if err := BindNativeClaudeHost(context.Background(), &input, Dependencies{NativeThinkingSummaryBeta: func(*cooperative.Scope, string) (*bool, error) { t.Error("unowned callback executed"); return nil, nil }}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("unowned callback admitted", err)
	}
}
