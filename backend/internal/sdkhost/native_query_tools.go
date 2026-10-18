package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type nativeQueryTools struct {
	pending bool
	batch   *queryloop.SerialToolBatch
}

func (h *Host) nativeInteractiveQueryTools() bool {
	return h.options.Owner != nil && h.options.Controller != nil && h.options.Print != nil && h.options.Print.NativeProcess && h.options.Print.NativeInteractive && !h.isAgent() && h.options.Print.Tools != nil && h.options.Print.Tools.Owns(h.options.Owner)
}

// REPL computeTools is sampled between iterations. Publishing AppState while
// a tool runs must not replace the Controller registry for its captured input.
func (h *Host) beginNativeQueryTools(scope *cooperative.Scope) func() error {
	if !h.nativeInteractiveQueryTools() {
		return func() error { return nil }
	}
	state := &nativeQueryTools{}
	h.nativeQueryTools = state
	return func() error {
		h.nativeQueryTools = nil
		if state.pending {
			return h.initializePrintCatalog(scope)
		}
		return nil
	}
}

func (h *Host) refreshNativeQueryTools(scope *cooperative.Scope, resolved *sessioncontroller.RawContext) error {
	state := h.nativeQueryTools
	if state == nil || !state.pending {
		return nil
	}
	if !scope.BelongsTo(h.options.Owner) || !h.nativeInteractiveQueryTools() || state.batch == nil {
		return cooperative.ErrRestricted
	}
	state.pending = false
	if err := h.initializePrintCatalog(scope); err != nil {
		return err
	}
	if err := state.batch.ReplaceRegisteredTools(scope, h.registered); err != nil {
		return err
	}
	resolved.Context.Tools = make([]protocol.JSONObject, len(h.originalTools))
	for i, tool := range h.originalTools {
		resolved.Context.Tools[i] = tool.Clone()
	}
	definitions := append([]toolschema.Definition{}, h.definitions...)
	resolved.Context.Host.NativeToolDefinitions = &definitions
	resolved.Context.Host.ToolSearchCatalogue = h.toolSearchCatalogue
	return nil
}
