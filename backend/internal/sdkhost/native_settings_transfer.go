package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeSettingsTransfer is minted only by the existing parent Host after it
// reads the actual AppState and merged cache and runs the original reference
// validation. Private fields prevent JSON or callers from fabricating proof.
// It carries immutable data; receiving owners retain no parent cache object.
type NativeSettingsTransfer struct {
	settings, merged, permission protocol.JSONObject
	proof                        *nativeSettingsProof
}
type nativeSettingsProof struct{ sameReference bool }

func (h *Host) TransferNativeSettings(scope *cooperative.Scope) (NativeSettingsTransfer, error) {
	if !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess {
		return NativeSettingsTransfer{}, cooperative.ErrRestricted
	}
	settings, permission, e := h.readASKSettings(scope)
	if e != nil {
		return NativeSettingsTransfer{}, e
	}
	merged, e := h.readASKMergedSettings()
	if e != nil {
		return NativeSettingsTransfer{}, e
	}
	if e = h.validateASKSettingsReference(settings, merged, scope); e != nil {
		return NativeSettingsTransfer{}, e
	}
	return NativeSettingsTransfer{settings: settings.Clone(), merged: merged.Clone(), permission: permission.Clone(), proof: &nativeSettingsProof{sameReference: settings == merged}}, nil
}

func (h *Host) readASKSettings(scope *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
	if h.options.NativeParentSettings == nil {
		return h.options.Controller.SDKSettingsOnOwner(scope)
	}
	transfer, e := h.options.NativeParentSettings(scope)
	if e != nil {
		return nil, protocol.JSONObject{}, e
	}
	if transfer.proof == nil {
		return nil, protocol.JSONObject{}, cooperative.ErrRestricted
	}
	settings, merged := transfer.settings.Clone(), transfer.merged.Clone()
	h.transferredASKSettings = &settings
	if transfer.proof.sameReference {
		h.transferredASKMerged = &settings
	} else {
		h.transferredASKMerged = &merged
	}
	h.transferredASKProof = transfer.proof
	permission, e := h.options.Controller.ApplyAgentPermissionOverlayOnOwner(scope, transfer.permission.Clone())
	return h.transferredASKSettings, permission, e
}

func (h *Host) readASKMergedSettings() (*protocol.JSONObject, error) {
	if h.options.NativeParentSettings != nil {
		if h.transferredASKProof == nil {
			return nil, cooperative.ErrRestricted
		}
		return h.transferredASKMerged, nil
	}
	return h.options.Startup.GetInitialSettings()
}

func (h *Host) validateTransferredASKSettings(settings, merged *protocol.JSONObject) bool {
	return h.options.NativeParentSettings != nil && h.transferredASKProof != nil && settings == h.transferredASKSettings && merged == h.transferredASKMerged
}
