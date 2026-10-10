package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

func (c *Controller) ConsumeSkillListingSuppression(scope *cooperative.Scope) (bool, error) {
	if c == nil || c.resume == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return false, cooperative.ErrRestricted
	}
	return c.resume.ConsumeSkillListingSuppression(), nil
}
