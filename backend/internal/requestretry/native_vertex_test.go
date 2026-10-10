package requestretry

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeVertexFactoryProofRejectsUnboundForeignAndStaleContext(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	factory := func(context.Context, string) (NativeVertexClient, error) { return NativeVertexClient{}, nil }
	if _, err := NewNativeVertex(owner, nil, factory); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil scope", err)
	}
	var binding *NativeVertex
	var stale *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		if _, err := NewNativeVertex(foreign, scope, factory); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign owner accepted")
		}
		if _, err := NewNativeVertex(owner, scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("missing actual factory accepted")
		}
		var err error
		binding, err = NewNativeVertex(owner, scope, factory, func(value string) (string, error) { return value, nil })
		if err != nil {
			return err
		}
		input := Input{Context: requestcontext.Context{Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: map[string]string{"CLAUDE_CODE_USE_VERTEX": "1"}}}}
		if err = BindNativeVertexHost(&input, Dependencies{NativeVertex: binding, StreamOwner: foreign, StreamScope: scope}); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign dependencies accepted")
		}
		if input.Context.Host.NativeVertexBound {
			return errors.New("failed bind modified host")
		}
		if err = BindNativeVertexHost(&input, Dependencies{NativeVertex: binding, StreamOwner: owner, StreamScope: scope}); err != nil {
			return err
		}
		if !input.Context.Host.NativeVertexBound || !input.Context.Host.NativeVertexProof.Current() {
			return errors.New("actual producer not bound")
		}
		input.Context.Host.Profile = requestcontext.DirectRelayProfile
		if err = BindNativeVertexHost(&input, Dependencies{NativeVertex: binding, StreamOwner: owner, StreamScope: scope}); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("unproved profile accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if binding.Owns(owner, stale) {
		t.Fatal("stale model ASK proof alive")
	}
	if _, err := binding.Client(context.Background(), "model"); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("stale factory ran", err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		input := Input{Context: requestcontext.Context{Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: map[string]string{"CLAUDE_CODE_USE_VERTEX": "1"}}}}
		if err := BindNativeVertexHost(&input, Dependencies{NativeVertex: binding, StreamOwner: foreign, StreamScope: scope}); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign current owner accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
