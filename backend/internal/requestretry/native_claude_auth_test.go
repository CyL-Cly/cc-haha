package requestretry

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"testing"
)

func TestNativeClaudeAuthRejectsUnboundForeignStaleAndFailedProducer(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	reads := 0
	subscriber := func(context.Context) (bool, error) { reads++; return true, nil }
	token := func(context.Context) (*string, error) { reads++; s := "fake-owned"; return &s, nil }
	if _, err := NewNativeClaudeAuth(owner, nil, subscriber, token); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil scope accepted", err)
	}
	var binding *NativeClaudeAuth
	var stale *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		if _, err := NewNativeClaudeAuth(foreign, scope, subscriber, token); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign owner accepted")
		}
		var err error
		binding, err = NewNativeClaudeAuth(owner, scope, subscriber, token)
		if err != nil {
			return err
		}
		input := standardInput()
		input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
		if err = BindNativeClaudeHost(context.Background(), &input, Dependencies{NativeClaudeAuth: binding, StreamOwner: foreign, StreamScope: scope}); !errors.Is(err, cooperative.ErrRestricted) || reads != 0 {
			return errors.New("foreign bind read credentials")
		}
		if err = BindNativeClaudeHost(context.Background(), &input, Dependencies{NativeClaudeAuth: binding, StreamOwner: owner, StreamScope: scope}); err != nil {
			return err
		}
		if !input.Context.Host.NativeClaudeSubscriber || !input.Context.Host.NativeClaudeProof.Current() {
			return errors.New("actual credential producer not bound")
		}
		if _, err = binding.Token(context.Background()); err != nil {
			return err
		}
		input.Context.Host.Profile = requestcontext.DirectRelayProfile
		if err = BindNativeClaudeHost(context.Background(), &input, Dependencies{NativeClaudeAuth: binding, StreamOwner: owner, StreamScope: scope}); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("finite caller gained OAuth")
		}
		failure := errors.New("actual subscriber producer failed")
		failing, _ := NewNativeClaudeAuth(owner, scope, func(context.Context) (bool, error) { return false, failure }, token)
		input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
		if err = BindNativeClaudeHost(context.Background(), &input, Dependencies{NativeClaudeAuth: failing, StreamOwner: owner, StreamScope: scope}); !errors.Is(err, failure) {
			return errors.New("producer error swallowed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if binding.Owns(owner, stale) || binding.Proof(owner, stale) != nil {
		t.Fatal("stale proof alive")
	}
	if _, err := binding.Token(context.Background()); !errors.Is(err, cooperative.ErrRestricted) || reads != 2 {
		t.Fatal("stale credential callback ran", err, reads)
	}
	// A scalar/token-shaped input without the actual native producer keeps the
	// original first-party admission guard, before any HTTP is possible.
	input := standardInput()
	input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
	input.Context.Host.Env["ANTHROPIC_BASE_URL"] = "https://api.anthropic.com"
	input.Context.Host.NativeClaudeSubscriber = true
	if _, err := Open(context.Background(), input, standardDeps()); err == nil {
		t.Fatal("unproved first-party context was admitted")
	}
}
