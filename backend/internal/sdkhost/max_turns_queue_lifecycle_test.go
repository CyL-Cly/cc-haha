package sdkhost_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

// Continuation demand must not invent an ASK for an empty queue, and shutdown
// must refuse further demand using the same Host/Controller/Owner. This is a Go
// lifecycle boundary, not a claim that Close substitutes for Source process exit.
func TestMaxTurnsSharedHostEmptyDemandAndShutdownRejectContinuation(t *testing.T) {
	checked := false
	_, result := originalBoundary(t, nativeControls{Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
		return o.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			if err := h.RunOne(scope, context.Background(), inputs[0]); err != nil {
				return err
			}
			before := len(h.History())
			entered, err := h.RunDequeued(scope, context.Background())
			if err != nil || entered || len(h.History()) != before {
				return fmt.Errorf("empty demand entered/mutated ASK: entered=%v err=%v", entered, err)
			}
			if err = h.Shutdown(scope); err != nil {
				return err
			}
			if err = h.RunOne(scope, context.Background(), inputs[1]); !errors.Is(err, cooperative.ErrClosed) {
				return fmt.Errorf("closed ASK admitted: %v", err)
			}
			entered, err = h.RunDequeued(scope, context.Background())
			if entered || !errors.Is(err, cooperative.ErrClosed) {
				return fmt.Errorf("closed queue entered: %v", err)
			}
			if _, err = h.AdmitParsedSDK(scope, inputs[1]); !errors.Is(err, cooperative.ErrClosed) {
				return fmt.Errorf("closed input admission reached Controller: %v", err)
			}
			checked = true
			return nil
		})
	}})
	if !checked || result["runError"] != nil || len(result["requests"].([]string)) != 1 {
		t.Fatal("shared lifecycle/physical request cardinality", checked, result["runError"], result["requests"])
	}
}
