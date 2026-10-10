package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) nativeSessionEnvironmentScript(ctx context.Context, scope *cooperative.Scope) (*string, error) {
	capture, err := m.nativeSessionEnvironmentCapture(scope)
	if err != nil {
		return nil, err
	}
	future, err := nativeSkillWork(m, scope, func() (*string, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return capture()
	})
	if err != nil {
		return nil, err
	}
	return future.Await(scope)
}

// Capture contains the actual process memo and immutable original inputs, not
// an owner Scope. Each native Bash job reads it before spawning its one process.
func (m *MainPrint) nativeSessionEnvironmentCapture(scope *cooperative.Scope) (func() (*string, error), error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.hookEnvironment == nil {
		return nil, mainCap("actual process session environment service")
	}
	sid := m.Controller.SessionID()
	if d := m.options.DetachedAgent; d != nil && d.preset != nil {
		var err error
		sid, err = nativeAgentOwnerCall(m, scope, d.preset.parent, func(*cooperative.Scope) (string, error) { return d.preset.parent.Controller.SessionID(), nil })
		if err != nil {
			return nil, err
		}
	}
	env := map[string]string{}
	for key, value := range m.nativeOpenAIEnvironment() {
		env[key] = value
	}
	service := m.hookEnvironment
	return func() (*string, error) { return service.Script(sid, env) }, nil
}
