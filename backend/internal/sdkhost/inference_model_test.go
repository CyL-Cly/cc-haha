package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeModelSelectionOwnerAndASKSnapshotIsolation(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	h := &Host{options: Options{Owner: owner, Model: "actual-old-model", Print: &PrintOptions{NativeProcess: true, EffortValue: json.RawMessage(`"low"`), EffortValueOverridesEnv: true}}}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		before := h.captureInferenceSettings()
		h.askRunning = true
		effort := json.RawMessage(`"high"`)
		if err := h.SetInferenceModel(scope, "actual-new-model", effort, true); err != nil {
			return err
		}
		effort[1] = 'X'
		current, err := h.InferenceModel(scope)
		if err != nil {
			return err
		}
		after := h.captureInferenceSettings()
		if current != "actual-new-model" || after.model != current || string(after.effort) != `"high"` || !after.effortOverridesEnv || before.model != "actual-old-model" || string(before.effort) != `"low"` || !before.effortOverridesEnv {
			return fmt.Errorf("active ASK inference snapshot mutated or next ASK not updated: before=%+v after=%+v", before, after)
		}
		after.effort[1] = 'Y'
		if string(h.options.Print.EffortValue) != `"high"` {
			return fmt.Errorf("snapshot aliased session effort")
		}
		if err := h.SetInferenceModel(scope, "third-model", nil, false); err != nil {
			return err
		}
		if string(h.options.Print.EffortValue) != `"high"` {
			return fmt.Errorf("model-only selection cleared sticky effort")
		}
		if err := h.SetInferenceModel(scope, "fourth-model", nil, true); err != nil {
			return err
		}
		if h.options.Print.EffortValue != nil || !h.options.Print.EffortValueOverridesEnv {
			return fmt.Errorf("undefined effort or original env-override policy changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := h.InferenceModel(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign getter admitted: %v", err)
		}
		if err := h.SetInferenceModel(scope, "foreign", nil, false); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign setter admitted: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeModelSelectionRejectsInvalidBeforeChangingModelAndRetainsFiniteGuard(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	h := &Host{options: Options{Owner: owner, Model: "original", Print: &PrintOptions{NativeProcess: true, EffortValue: json.RawMessage(`"low"`)}}}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, raw := range []string{`null`, ` null `, `true`, `"unknown"`, `"1"`, `1.5`, `{}`, `[]`, `1e999`} {
			if err := h.SetInferenceModel(scope, "must-not-change", []byte(raw), true); err == nil {
				return fmt.Errorf("invalid effort admitted: %s", raw)
			}
			if h.options.Model != "original" || string(h.options.Print.EffortValue) != `"low"` {
				return fmt.Errorf("rejected effort changed model/session")
			}
		}
		if err := h.SetInferenceModel(scope, "", nil, false); err == nil {
			return fmt.Errorf("empty unresolved model admitted")
		}
		h.options.Print.NativeProcess = false
		if err := h.SetInferenceModel(scope, "finite-change", nil, false); err == nil {
			return fmt.Errorf("finite static profile broadened")
		}
		if _, err := h.InferenceModel(scope); err == nil {
			return fmt.Errorf("finite mutable domain admitted")
		}
		h.options.Print.NativeProcess = true
		h.closing = true
		if err := h.SetInferenceModel(scope, "closed-change", nil, false); !errors.Is(err, cooperative.ErrClosed) {
			return fmt.Errorf("closed host setter admitted: %v", err)
		}
		if h.options.Model != "original" {
			return fmt.Errorf("failed transitions changed model")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
