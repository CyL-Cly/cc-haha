package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

func (m *MainPrint) runNativeCLISetup(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.agentChild || m.teamWorker != nil {
		return nil
	}
	a := m.options.Arguments
	// The frozen main action passes setupTrigger only to runHeadless. Its
	// init-only branch runs independently of --print and forces both events
	// synchronous; interactive --init/--maintenance do not invoke Setup.
	if !a.InitOnly && !m.options.PublicPrintLoadErrors {
		return nil
	}
	trigger := ""
	if a.InitOnly || a.Init {
		trigger = "init"
	} else if a.Maintenance {
		trigger = "maintenance"
	}
	if trigger == "" {
		return nil
	}
	base, err := m.nativeSessionStartBase(scope, m.Controller.SessionID())
	if err != nil {
		return err
	}
	base.Set("trigger", quoteMain(trigger))
	// executeSetupHooks calls createBaseHookInput(undefined), even when
	// the CLI's real active permission mode is bypass/auto/plan.
	base.Set("permission_mode", nil)
	call := queryhooks.Context{Scope: scope, ForceSyncExecution: a.InitOnly}
	return m.hook.ExecuteCommandEvent(context.Background(), queryhooks.CommandEvent{Event: "Setup", SessionID: m.Controller.SessionID(), ToolUseID: uuidMain(), MatchQuery: trigger, Fields: base, Call: call}, func(queryhooks.EventResult) error { return nil })
}

func (m *MainPrint) CLIInitOnly() bool { return m.options.Arguments.InitOnly }
