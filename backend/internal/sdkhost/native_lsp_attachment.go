package sdkhost

import "encoding/json"

func (h *Host) retainedNativeLSPAttachment(payload json.RawMessage) bool {
	return h != nil && h.options.Print != nil && h.options.Print.NativeProcess && h.options.Print.Tools != nil && h.options.Print.Tools.Owns(h.options.Owner) && h.options.Print.Tools.HasLSPBindings() && object(payload).String("type") == "diagnostics"
}
