package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
)

// BeginTerminalResumePlanOnOwner preserves the fire-and-forget caller. Errors
// still belong to the original copy helper's diagnostic consumer.
func (c *Controller) BeginTerminalResumePlanOnOwner(scope *cooperative.Scope, value *TerminalResume) error {
	if err := c.checkTerminalResume(scope, value); err != nil {
		return err
	}
	if value == nil || value.stage != 0 {
		return cooperative.ErrRestricted
	}
	log, err := resumestate.DecodeOriginalLog(value.log)
	if err != nil {
		return err
	}
	future, err := c.resume.BeginTerminalPlanRestore(scope, log, value.log.Get("sessionId"))
	if err != nil {
		return err
	}
	_, err = future.Then(scope, func(_ *cooperative.Scope, _ bool, failure error) error {
		if failure != nil {
			c.options.ResumeOptions.OnError(failure)
		}
		return nil
	})
	return err
}

func (c *Controller) BeginTerminalResumeFileHistoryOnOwner(scope *cooperative.Scope, value *TerminalResume) error {
	if err := c.checkTerminalResume(scope, value); err != nil {
		return err
	}
	if value == nil || value.stage != 1 || c.nativeFileHistory == nil {
		return cooperative.ErrRestricted
	}
	if value.log.Get("fileHistorySnapshots") == nil {
		return nil
	}
	log, err := resumestate.DecodeOriginalLog(value.log)
	if err != nil {
		return err
	}
	future, err := c.resume.BeginTerminalFileHistoryRestore(scope, log.History, c.nativeFileHistory)
	if err != nil {
		return err
	}
	_, err = future.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error {
		if failure != nil {
			c.options.ResumeOptions.OnError(failure)
		}
		return nil
	})
	return err
}
