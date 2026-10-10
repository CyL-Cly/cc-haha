package clibootstrap

import (
	"context"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionplans"
)

func (m *MainPrint) newNativePlans() error {
	if m.plans != nil || m.nativeSettings == nil {
		return nil
	}
	config := m.options.Native.Environment["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(m.options.Native.Home, ".claude")
	}
	p, e := sessionplans.New(sessionplans.Options{Bootstrap: m.state, ConfigDir: config, CWD: m.options.Native.CWD, InitialSettings: m.Startup.GetInitialSettings, OnError: func(e error) { _, _ = m.options.Error.Write([]byte(e.Error() + "\n")) }})
	if e == nil {
		m.plans = p
	}
	return e
}
func (m *MainPrint) Plans() *sessionplans.Service { return m.plans }
func (m *MainPrint) PlanInterviewPhase() (bool, error) {
	env := m.options.Native.Environment
	if env["USER_TYPE"] == "ant" {
		return true, nil
	}
	if envTruthyNative(env["CLAUDE_CODE_PLAN_MODE_INTERVIEW_PHASE"]) {
		return true, nil
	}
	// The native external profile has no remote feature-store producer. This
	// is the original cached feature's default, or an explicit env false.
	return false, nil
}
func (m *MainPrint) checkNativePlanInactive(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}

	return nil
}
func (m *MainPrint) nativePlanModeBindings() *builtintools.PlanModeBindings {
	planAgentID := m.options.AgentID
	if m.teamWorker != nil {
		planAgentID = ""
	}
	return &builtintools.PlanModeBindings{Teammate: m.nativePlanTeammateIdentity, RequestApproval: m.nativeRequestTeamPlanApproval, Plans: m.plans, AgentID: planAgentID, CheckInactive: m.checkNativePlanInactive, InterviewPhase: m.PlanInterviewPhase, Enter: func(scope *cooperative.Scope) error { return m.transitionNativePlan(scope, true) }, Exit: func(scope *cooperative.Scope) error { return m.transitionNativePlan(scope, false) }, HasTaskTool: func(scope *cooperative.Scope) (bool, error) {
		if !scope.BelongsTo(m.Owner) {
			return false, cooperative.ErrRestricted
		}
		env := m.options.Native.Environment
		if v, exists := env["CC_HAHA_AGENT_TEAMS_ENABLED"]; exists && !envTruthyNative(v) {
			return false, nil
		}
		optIn := env["CC_HAHA_AGENT_TEAMS_ENABLED"]
		if _, exists := env["CC_HAHA_AGENT_TEAMS_ENABLED"]; !exists {
			optIn = env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"]
			if _, exists := env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"]; !exists {
				optIn = env["CC_HAHA_AGENT_TEAMS_DEFAULT"]
			}
		}
		if env["USER_TYPE"] != "ant" && !envTruthyNative(optIn) {
			return false, nil
		}
		// ExitPlanMode's result checks the current executable context for
		// Agent. Teammate plan approval is a separate earlier call branch.
		return m.Host.HasRegisteredTool(scope, "Agent")
	}}
}
func (m *MainPrint) transitionNativePlan(scope *cooperative.Scope, enter bool) error {
	before, e := m.Controller.PlanModeContext(scope)
	if e != nil {
		return e
	}
	if enter {
		e = m.Host.EnterNativePlanMode(scope)
	} else {
		e = m.Host.ExitNativePlanMode(scope)
	}
	if e != nil {
		return e
	}
	after, e := m.Controller.PlanModeContext(scope)
	if e != nil {
		return e
	}
	if before.String("mode") != after.String("mode") && m.options.OnPermissionModeChanged != nil {
		return m.options.OnPermissionModeChanged(context.Background(), after.String("mode"))
	}
	return nil
}
