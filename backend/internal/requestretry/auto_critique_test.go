package requestretry

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestCritiquePurposeCannotAuthorizeASKClassifierOrForeignOwner(t *testing.T) {
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		authority, err := cooperative.NewNativeAutoCritiqueModels(scope, owner)
		if err != nil {
			return err
		}
		for _, source := range []string{"sdk", "auto_mode", "model_validation", "auto_mode_critique"} {
			input := Input{Context: requestcontext.Context{Options: requestcontext.Options{Model: "fixture-model", QuerySource: source}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile}}}
			deps := Dependencies{StreamOwner: owner, StreamScope: scope}
			if source == "auto_mode_critique" {
				deps.StreamOwner = other
			}
			if _, err := RunNativeAutoCritique(context.Background(), input, protocol.NewJSONObject(), requestcontext.SideQueryInput{}, deps, authority); !errors.Is(err, cooperative.ErrRestricted) {
				t.Fatal("wrong purpose or command owner admitted request", source, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
