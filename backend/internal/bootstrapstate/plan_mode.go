package bootstrapstate

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (state *State) PlanModeFlags(scope *cooperative.Scope) (hasExited, needsExit bool, err error) {
	if err = state.CheckScope(scope); err != nil {
		return
	}
	return state.hasExitedPlanMode, state.needsPlanModeExitAttachment, nil
}
func (state *State) SetNeedsPlanModeExitAttachment(scope *cooperative.Scope, value bool) error {
	if e := state.CheckScope(scope); e != nil {
		return e
	}
	state.needsPlanModeExitAttachment = value
	return nil
}
func (state *State) SetHasExitedPlanMode(scope *cooperative.Scope, value bool) error {
	if e := state.CheckScope(scope); e != nil {
		return e
	}
	state.hasExitedPlanMode = value
	return nil
}
func (state *State) HandlePlanModeTransition(scope *cooperative.Scope, from, to string) error {
	if e := state.CheckScope(scope); e != nil {
		return e
	}
	if to == "plan" && from != "plan" {
		state.needsPlanModeExitAttachment = false
	}
	if from == "plan" && to != "plan" {
		state.needsPlanModeExitAttachment = true
	}
	return nil
}

func (state *State) SetNeedsAutoModeExitAttachment(scope *cooperative.Scope, value bool) error {
	if e := state.CheckScope(scope); e != nil {
		return e
	}
	state.needsAutoModeExitAttachment = value
	return nil
}
func (state *State) TakeAutoModeExitAttachment(scope *cooperative.Scope, active bool) (bool, error) {
	if e := state.CheckScope(scope); e != nil {
		return false, e
	}
	need := state.needsAutoModeExitAttachment
	state.needsAutoModeExitAttachment = false
	return need && !active, nil
}
