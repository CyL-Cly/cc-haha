package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func (m *MainPrint) nativeSDKInferenceControl(scope *cooperative.Scope, request protocol.JSONObject) error {
	if !scope.BelongsTo(m.Owner) || m.Host == nil {
		return cooperative.ErrRestricted
	}
	switch request.String("subtype") {
	case "set_model":
		requested := "default"
		if value := request.Get("model"); value != nil && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			var err error
			requested, err = sdkInitializeString(value)
			if err != nil {
				return err
			}
		}
		if err := m.applyNativeSessionModelOnOwner(context.Background(), scope, requested); err != nil {
			return err
		}
		if m.remoteIO != nil {
			model, err := m.Host.InferenceModel(scope)
			if err != nil {
				return err
			}
			m.remoteIO.ReportMetadata(protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quoteMain(model)}))
		}
		return nil
	case "set_max_thinking_tokens":
		value := bytes.TrimSpace(request.Get("max_thinking_tokens"))
		thinking := requestcontext.Thinking{}
		if !bytes.Equal(value, []byte("null")) {
			var tokens float64
			if len(value) > 0 {
				if err := json.Unmarshal(value, &tokens); err != nil {
					return err
				}
			}
			if tokens == 0 && len(value) > 0 {
				thinking.Type = "disabled"
			} else {
				thinking.Type = "enabled"
				if len(value) > 0 {
					thinking.BudgetTokens = &tokens
				}
			}
		}
		if err := m.Host.SetNativeSDKThinking(scope, thinking); err != nil {
			return err
		}
		m.options.Arguments.Thinking = thinking
		return nil
	}
	return cooperative.ErrRestricted
}
