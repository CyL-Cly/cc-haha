package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// SDK initialization reuses the same compiled schema, transient tool and
// canonical catalog as the CLI option. No tool is installed without that real
// parent schema binding, and later child catalogs do not inherit it.
func (h *Host) InstallNativeSDKJSONSchema(scope *cooperative.Scope, schema json.RawMessage) (*NativeJSONSchema, error) {
	if e := h.checkInferenceOwner(scope); e != nil {
		return nil, e
	}
	resources, e := h.options.Controller.BorrowNativeCopyBinding(h.options.Owner)
	if e != nil {
		return nil, e
	}
	binding, e := NewNativeJSONSchema(h.options.Owner, resources, schema)
	if e != nil {
		return nil, e
	}
	h.options.Print.NativeJSONSchema = binding
	h.options.Print.NativeSynthetic = binding.Synthetic()
	if e = h.initializePrintCatalog(scope); e != nil {
		return nil, e
	}
	return binding, nil
}
func (h *Host) SetNativeSDKAppendPrompt(scope *cooperative.Scope, value string) error {
	if e := h.checkInferenceOwner(scope); e != nil {
		return e
	}
	h.options.Print.AppendSystemPrompt = value
	return nil
}
