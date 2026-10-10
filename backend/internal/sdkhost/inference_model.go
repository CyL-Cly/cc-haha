package sdkhost

import (
	"bytes"
	"encoding/json"
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type inferenceSettings struct {
	model              string
	effort             json.RawMessage
	effortOverridesEnv bool
}

func (h *Host) captureInferenceSettings() inferenceSettings {
	result := inferenceSettings{model: h.options.Model}
	if h.options.Print != nil {
		result.effort = append(json.RawMessage(nil), h.options.Print.EffortValue...)
		result.effortOverridesEnv = h.options.Print.EffortValueOverridesEnv
	}
	return result
}
func (h *Host) checkInferenceOwner(scope *cooperative.Scope) error {
	if h == nil || !scope.BelongsTo(h.options.Owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); err != nil {
		return err
	}
	if h.closing {
		return cooperative.ErrClosed
	}
	if h.options.Print == nil || !h.options.Print.NativeProcess {
		return cap("mutable native inference model profile")
	}
	return nil
}

// InferenceModel is the resolved base model for the next ASK. The caller owns
// Source raw AppState selection, allowlist checks and settings persistence.
func (h *Host) InferenceModel(scope *cooperative.Scope) (string, error) {
	if err := h.checkInferenceOwner(scope); err != nil {
		return "", err
	}
	return h.options.Model, nil
}

func (h *Host) InferenceEffort(scope *cooperative.Scope) (json.RawMessage, bool, error) {
	if err := h.checkInferenceOwner(scope); err != nil {
		return nil, false, err
	}
	return append(json.RawMessage(nil), h.options.Print.EffortValue...), h.options.Print.EffortValueOverridesEnv, nil
}

// SetInferenceModel changes only the next ASK's captured inference settings.
// Raw aliases/null and mainLoopModelForSession are deliberately not inferred
// from the resolved API model. Effort's original env-override policy is retained.
func (h *Host) SetInferenceModel(scope *cooperative.Scope, resolved string, effort json.RawMessage, updateEffort bool) error {
	if err := h.checkInferenceOwner(scope); err != nil {
		return err
	}
	if resolved == "" {
		return cap("empty resolved native inference model")
	}
	if updateEffort && effort != nil {
		valid := false
		var level string
		if json.Unmarshal(effort, &level) == nil {
			for _, choice := range []string{"low", "medium", "high", "xhigh", "max"} {
				valid = valid || level == choice
			}
		} else {
			var number float64
			valid = json.Unmarshal(effort, &number) == nil && !bytes.Equal(bytes.TrimSpace(effort), []byte("null")) && math.Trunc(number) == number && !math.IsInf(number, 0) && !math.IsNaN(number)
		}
		if !valid {
			return cap("actual native effort value")
		}
	}
	h.options.Model = resolved
	if updateEffort {
		h.options.Print.EffortValue = append(json.RawMessage(nil), effort...)
	}
	return nil
}
