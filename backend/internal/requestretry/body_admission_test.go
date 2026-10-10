package requestretry

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
)

func TestNativeBodyAwaitInvalidTargetsRejectBeforePreparation(t *testing.T) {
	for _, name := range []string{"no-native-factory", "legacy-and-native-body"} {
		t.Run(name, func(t *testing.T) {
			input, deps := standardInput(), standardDeps()
			awaits, prepare, keys, clients, uuid := 0, 0, 0, 0, 0
			deps.BodyIOAwait = func(provider.OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
				awaits++
				return nil, nil
			}
			if name == "legacy-and-native-body" {
				deps.NativeOpenIO = providernative.NewFactory(nil, nil, nil)
				deps.OpenIOAwait = deps.BodyIOAwait
			}
			deps.OnPreparation = func([]protocol.JSONObject) { prepare++ }
			deps.OnKeyLookup = func() { keys++ }
			deps.OnClient = func() { clients++ }
			deps.UUID = func() string { uuid++; return "unadmitted" }
			_, err := Open(context.Background(), input, deps)
			var cap *CapabilityError
			if !errors.As(err, &cap) || cap.Boundary != "native body owner requires one original native headers bridge" || awaits+prepare+keys+clients+uuid != 0 {
				t.Fatalf("unsupported native body input did effects: %v counts=%d/%d/%d/%d/%d", err, awaits, prepare, keys, clients, uuid)
			}
		})
	}
}
