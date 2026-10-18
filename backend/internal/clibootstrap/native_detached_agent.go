package clibootstrap

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// The detached child receives values from its parent's actual producer. Every
// callback crosses through an owner-local native future; it never borrows a
// foreign Bootstrap, Scope, permission State, or accounting Session.
type NativeDetachedAgentOptions struct {
	agentSummary            bool
	inheritedFileCache      []builtinwrite.CachedFile
	isBackgrounded          func() bool
	backgroundToolNames     map[string]bool
	sourceMCP               *NativeMCPToolContext
	authParent              *MainPrint
	httpPool                *providernative.PoolTransfer
	workflowTaskViewParent  bool
	Bootstrap               bootstrapstate.Snapshot
	PromptSections          []json.RawMessage
	PermissionMode          string
	OmitClaudeMd            bool
	TaskDirectory           string
	AgentType               string
	bindTasks               func(*MainPrint) *builtintools.BackgroundTaskBindings
	liveSettings            func(*MainPrint, *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error)
	accountingRelay         func(*MainPrint, *cooperative.Scope, modelaccounting.LedgerDelta) error
	takeQueued              func(*MainPrint, *cooperative.Scope) ([]protocol.JSONObject, error)
	pruneQueued             func(*MainPrint, *cooperative.Scope, string) error
	transferSettings        func(*MainPrint, *cooperative.Scope) (sdkhost.NativeSettingsTransfer, error)
	resolvePermission       nativeAgentPermissionResolver
	userImageAuthorized     func(*MainPrint, *cooperative.Scope, string) (bool, error)
	rememberUserImage       func(*MainPrint, *cooperative.Scope, string) error
	iteratorReady           func(*cooperative.Scope) error
	restartPrompt           *sdkhost.NativeAgentRestartPrompt
	synthetic               *nativeWorkflowSynthetic
	preset                  *nativeAgentPreset
	teammate                *sessioncontroller.InProcessTeammateIdentity
	teammateShutdownPending bool
}

func (m *MainPrint) nativeDetachedControllerOptions(o sessioncontroller.Options) (sessioncontroller.Options, error) {
	d := m.options.DetachedAgent
	if d == nil {
		return o, nil
	}
	o.WriterOptions.AgentID = m.options.AgentID
	o.WriterOptions.CWD = d.Bootstrap.OriginalCWD
	o.QuerySource = m.options.QuerySource
	o.RestoreCosts = nil
	o.AgentPermissionMode = d.PermissionMode
	o.AgentOmitClaudeMd = d.OmitClaudeMd
	o.CustomSystemPrompt = nil
	o.ResolveSystemPromptForModel = nil
	sections := make([]json.RawMessage, len(d.PromptSections))
	for i, v := range d.PromptSections {
		sections[i] = append(json.RawMessage(nil), v...)
	}
	o.ResolveSystemPrompt = func(context.Context, []protocol.JSONObject) ([]json.RawMessage, error) {
		out := make([]json.RawMessage, len(sections))
		for i, v := range sections {
			out[i] = append(json.RawMessage(nil), v...)
		}
		return out, nil
	}
	// This child already captured its original Agent prompt before construction.
	// Its pending prefix must observe those same sections, not the parent's
	// default prompt builder. Preserve the original fulfilled async result.
	o.BeginNativeDefaultPrompt = func(scope *cooperative.Scope, _ []protocol.JSONObject, _ string) (*ownerasync.OwnerFuture[[]json.RawMessage], error) {
		if _, _, err := m.nativePromptProducer(scope); err != nil {
			return nil, err
		}
		future, err := ownerasync.NewOwnerFuture[[]json.RawMessage](m.Owner)
		if err != nil {
			return nil, err
		}
		out := make([]json.RawMessage, len(sections))
		for i, section := range sections {
			out[i] = append(json.RawMessage(nil), section...)
		}
		return future, future.Resolve(scope, out)
	}
	o.LiveSDKSettings = func(scope *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
		return d.liveSettings(m, scope)
	}
	if d.teammate != nil {
		err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var err error
			o.InProcessTeammateContext, err = sessioncontroller.NewInProcessTeammateContext(scope, m.Owner, m.options.AgentID, *d.teammate)
			return err
		})
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

func (m *MainPrint) nativeParentSettingsProvider() func(*cooperative.Scope) (sdkhost.NativeSettingsTransfer, error) {
	if d := m.options.DetachedAgent; d != nil {
		return func(scope *cooperative.Scope) (sdkhost.NativeSettingsTransfer, error) {
			return d.transferSettings(m, scope)
		}
	}
	return nil
}

func (m *MainPrint) initializeDetachedAgent(scope *cooperative.Scope) error {
	d := m.options.DetachedAgent
	if d == nil || !m.detachedConstructed {
		return nil
	}
	if !scope.BelongsTo(m.Owner) || m.options.AgentID == "" || d.bindTasks == nil || d.liveSettings == nil || d.accountingRelay == nil || d.transferSettings == nil || d.takeQueued == nil || d.pruneQueued == nil || d.TaskDirectory == "" || m.options.Arguments.SessionID != d.Bootstrap.SessionID || !(m.options.QuerySource == "agent:custom" || strings.HasPrefix(m.options.QuerySource, "agent:builtin:") || m.options.QuerySource == "workflow_agent" && d.AgentType != "") {
		return cooperative.ErrRestricted
	}
	if e := m.state.SwitchSession(scope, d.Bootstrap.SessionID, d.Bootstrap.SessionProjectDir); e != nil {
		return e
	}
	if e := m.state.SetOriginalCWD(scope, d.Bootstrap.OriginalCWD); e != nil {
		return e
	}
	if e := m.state.SetProjectRoot(scope, d.Bootstrap.ProjectRoot); e != nil {
		return e
	}
	if e := m.state.SetCWD(scope, m.options.Native.CWD); e != nil {
		return e
	}
	if d.authParent != nil {
		if e := m.Controller.BindNativeParentSessionOnOwner(scope, d.authParent.state); e != nil {
			return e
		}
	}
	agentType := strings.TrimPrefix(m.options.QuerySource, "agent:builtin:")
	if d.AgentType != "" {
		agentType = d.AgentType
	}
	context := protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: quoteMain(m.options.AgentID)}, protocol.JSONField{Name: "agentType", Value: quoteMain(agentType)})
	if d.teammate != nil {
		identity, err := m.Controller.TeammateIdentityOnOwner(scope)
		if err != nil || identity != *d.teammate {
			return cooperative.ErrRestricted
		}
		context = protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: quoteMain(identity.AgentID)}, protocol.JSONField{Name: "parentSessionId", Value: quoteMain(identity.ParentSessionID)}, protocol.JSONField{Name: "agentName", Value: quoteMain(identity.AgentName)}, protocol.JSONField{Name: "teamName", Value: quoteMain(identity.TeamName)}, protocol.JSONField{Name: "planModeRequired", Value: rawMain(identity.PlanModeRequired)}, protocol.JSONField{Name: "isTeamLead", Value: rawMain(false)}, protocol.JSONField{Name: "agentType", Value: quoteMain("teammate")}, protocol.JSONField{Name: "invocationKind", Value: quoteMain("spawn")}, protocol.JSONField{Name: "invocationEmitted", Value: rawMain(false)})
		if identity.Color != "" {
			context.Set("agentColor", quoteMain(identity.Color))
		}
	}
	binding, e := modelaccounting.NewAgentContext(m.Owner, context)
	if e != nil {
		return e
	}
	m.options.AgentContext = binding
	accounting, e := m.Controller.ModelAccountingFor(m.Owner)
	if e != nil {
		return e
	}
	return accounting.SetLedgerRelay(scope, func(scope *cooperative.Scope, delta modelaccounting.LedgerDelta) error {
		return d.accountingRelay(m, scope, delta)
	})
}

func (m *MainPrint) nativeInProcessTeammateIdentity() *sessioncontroller.InProcessTeammateIdentity {
	if d := m.options.DetachedAgent; d != nil {
		return d.teammate
	}
	return nil
}

func (m *MainPrint) nativeAgentIteratorReady() func(*cooperative.Scope) error {
	if d := m.options.DetachedAgent; d != nil {
		return d.iteratorReady
	}
	return nil
}
