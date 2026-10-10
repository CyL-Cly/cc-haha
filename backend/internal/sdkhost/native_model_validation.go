package sdkhost

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func (h *Host) ValidateNativeModel(ctx context.Context, scope *cooperative.Scope, model string, settings protocol.JSONObject) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	if !h.nativeModelValidationModels.Owns(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	return h.nativeModelValidationModels.Run(scope, ctx, func(producer *cooperative.Scope) error {
		device, err := h.requestDeviceID(producer)
		if err != nil {
			return err
		}
		input := requestretry.Input{Context: requestcontext.Context{Options: requestcontext.Options{Model: model, QuerySource: "model_validation"}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: device, SessionID: h.options.Controller.SessionID()}}, Runtime: h.options.Runtime}
		deps := h.requestDependencies(producer)
		if h.options.NativeValidationAuth != nil {
			deps.NativeClaudeAuth, deps.NativeClaudeError = h.options.NativeValidationAuth(producer)
		}
		return requestretry.ValidateNativeModel(ctx, input, settings, deps, h.nativeModelValidationModels)
	})
}
