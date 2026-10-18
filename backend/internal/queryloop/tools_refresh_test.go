package queryloop

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestRegisteredToolRefreshRetainsTheActualBatchOwnerAndExecutableGuards(t *testing.T) {
	h := newBatchHarness(t)
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	var batch *SerialToolBatch
	var retained *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		retained = scope
		h.Capabilities.Owner, h.Capabilities.Scope = owner, scope
		var err error
		batch, err = NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
		if err != nil {
			return err
		}
		if err := batch.ReplaceRegisteredTools(nil, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("nil scope accepted", err)
		}
		invalid := h.Tool
		invalid.IsMCP = true
		if err := batch.ReplaceRegisteredTools(scope, []RegisteredTool{invalid}); err == nil {
			t.Error("unbound MCP accepted")
		}
		updated := h.Tool
		updated.Name = "RefreshedWrite"
		if err := batch.ReplaceRegisteredTools(scope, []RegisteredTool{updated}); err != nil {
			return err
		}
		if len(batch.tools) != 1 || batch.tools[0].Name != updated.Name {
			t.Error("actual refreshed executable missing")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := batch.ReplaceRegisteredTools(retained, nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained scope accepted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := batch.ReplaceRegisteredTools(scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign scope accepted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := NewSerialToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.ReplaceRegisteredTools(retained, nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("finite serial profile accepted replacement", err)
	}
}
