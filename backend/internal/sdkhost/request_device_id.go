package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (h *Host) requestDeviceID(scope *cooperative.Scope) (string, error) {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Global == nil {
		return "", cap("original device ID owner/state")
	}
	raw, err := h.options.Global.GetOrCreateUserID()
	if err != nil {
		return "", err
	}
	var device string
	if json.Unmarshal(raw, &device) != nil || device == "" {
		return "", cap("original device ID string state")
	}
	return device, nil
}
