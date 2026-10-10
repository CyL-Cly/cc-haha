package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type NativeTypedModelState struct {
	Base, ForSession *string
	FastMode         bool
}

func typedModelPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func (c *Controller) checkTypedModelOwner(scope *cooperative.Scope) error {
	if c == nil || c.options.Bootstrap == nil || !c.active {
		return cooperative.ErrRestricted
	}
	return c.options.Bootstrap.CheckScope(scope)
}

// InitializeTypedModelState receives the real startup raw model and fast-mode
// initializer once; later settings refreshes do not recreate React AppState.
func (c *Controller) InitializeTypedModelState(scope *cooperative.Scope, base *string, fast bool) error {
	if err := c.checkTypedModelOwner(scope); err != nil {
		return err
	}
	if c.appState.typedModel != nil {
		return capability("repeated native typed model initializer")
	}
	c.appState.typedModel = &NativeTypedModelState{Base: typedModelPointer(base), FastMode: fast}
	c.appState.fastMode = &c.appState.typedModel.FastMode
	return nil
}
func (c *Controller) TypedModelStateOnOwner(scope *cooperative.Scope) (NativeTypedModelState, error) {
	if err := c.checkTypedModelOwner(scope); err != nil {
		return NativeTypedModelState{}, err
	}
	if c.appState.typedModel == nil {
		return NativeTypedModelState{}, capability("uninitialized native typed model AppState")
	}
	s := *c.appState.typedModel
	s.Base = typedModelPointer(s.Base)
	s.ForSession = typedModelPointer(s.ForSession)
	return s, nil
}
func (c *Controller) ApplyTypedModelState(scope *cooperative.Scope, base *string, fast bool) error {
	if err := c.checkTypedModelOwner(scope); err != nil {
		return err
	}
	if c.appState.typedModel == nil {
		return capability("uninitialized native typed model AppState")
	}
	c.appState.typedModel.Base = typedModelPointer(base)
	c.appState.typedModel.ForSession = nil
	c.appState.typedModel.FastMode = fast
	return nil
}

// ApplyReplModelOverride is the REPL bridge state producer. SDK set_model's
// base selection and local breadcrumbs remain a separate control consumer.
func (c *Controller) ApplyReplModelOverride(scope *cooperative.Scope, model *string) error {
	if err := c.checkTypedModelOwner(scope); err != nil {
		return err
	}
	if c.appState.typedModel == nil {
		return capability("uninitialized native typed model AppState")
	}
	c.appState.typedModel.ForSession = typedModelPointer(model)
	return nil
}

// SetTypedFastMode is the original notification hook's AppState update. It
// leaves the selected base/session models and persisted preference untouched.
func (c *Controller) SetTypedFastMode(scope *cooperative.Scope, enabled bool) error {
	if err := c.checkTypedModelOwner(scope); err != nil {
		return err
	}
	if c.appState.typedModel == nil {
		return capability("uninitialized native typed model AppState")
	}
	c.appState.typedModel.FastMode = enabled
	return nil
}
