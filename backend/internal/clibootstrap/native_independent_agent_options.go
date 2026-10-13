package clibootstrap

import (
	"encoding/json"
	"io"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

// Foreground and background children use the same existing MainPrint constructor
// and original parent authorities. These callbacks move immutable values between
// the actual Owners; settings, accounting, tasks and attachments keep one producer.
func (m *MainPrint) nativeIndependentAgentOptions(scope *cooperative.Scope, request builtintools.AgentRequest, agentID, cwd, model, directory string, sections []json.RawMessage, restartPrompt *sdkhost.NativeAgentRestartPrompt) (MainPrintOptions, error) {
	if !scope.BelongsTo(m.Owner) {
		return MainPrintOptions{}, cooperative.ErrRestricted
	}
	parentLedger, e := m.Controller.ModelAccountingFor(m.Owner)
	if e != nil {
		return MainPrintOptions{}, e
	}
	snapshot := m.state.Snapshot()
	detached := &NativeDetachedAgentOptions{authParent: m, Bootstrap: snapshot, PromptSections: sections, PermissionMode: request.PermissionModeOverride, OmitClaudeMd: request.OmitClaudeMd, TaskDirectory: directory}
	background := agentcatalog.ResolveTools(agentcatalog.Definition{Name: request.AgentType, Source: request.Source, Fields: request.SelectedAgent}, request.Tools, true, false)
	detached.backgroundToolNames = map[string]bool{}
	for _, name := range background.ResolvedTools {
		detached.backgroundToolNames[name] = true
	}
	m.bindAgentImageAuthorization(detached)
	detached.resolvePermission = m.independentAgentPermissionResolver()
	detached.restartPrompt = restartPrompt
	detached.bindTasks = func(child *MainPrint) *builtintools.BackgroundTaskBindings {
		return m.detachedTaskBindings(child, directory)
	}
	detached.transferSettings = func(child *MainPrint, scope *cooperative.Scope) (sdkhost.NativeSettingsTransfer, error) {
		return nativeAgentOwnerCall(child, scope, m, m.Host.TransferNativeSettings)
	}
	detached.liveSettings = func(child *MainPrint, scope *cooperative.Scope) (*protocol.JSONObject, protocol.JSONObject, error) {
		type settingsView struct {
			settings   *protocol.JSONObject
			permission protocol.JSONObject
		}
		v, e := nativeAgentOwnerCall(child, scope, m, func(scope *cooperative.Scope) (settingsView, error) {
			settings, permission, e := m.Controller.SDKSettingsOnOwner(scope)
			var copy *protocol.JSONObject
			if settings != nil {
				value := settings.Clone()
				copy = &value
			}
			return settingsView{copy, permission.Clone()}, e
		})
		return v.settings, v.permission, e
	}
	detached.accountingRelay = func(child *MainPrint, scope *cooperative.Scope, d modelaccounting.LedgerDelta) error {
		copy := d.Clone()
		return bridgeAgentVoid(child, m, scope, func(scope *cooperative.Scope) error { return parentLedger.ApplyLedgerDelta(scope, copy) })
	}
	detached.takeQueued = func(child *MainPrint, scope *cooperative.Scope) ([]protocol.JSONObject, error) {
		return nativeAgentOwnerCall(child, scope, m, func(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
			return m.Controller.TakeNativeQueuedAttachmentsOnOwner(scope, child.options.AgentID)
		})
	}
	detached.pruneQueued = func(child *MainPrint, scope *cooperative.Scope, id string) error {
		return bridgeAgentVoid(child, m, scope, func(scope *cooperative.Scope) error {
			return m.Controller.PruneAgentTaskNotificationsOnOwner(scope, id)
		})
	}
	o := m.options
	o.DetachedAgent = detached
	o.Native = m.options.Native.agentScopedBoundary(cwd)
	o.AgentID = agentID
	o.AgentContext = nil
	o.QuerySource = "agent:custom"
	if request.Source == "built-in" || request.Source == "" {
		o.QuerySource = "agent:builtin:" + request.AgentType
	}
	tools := append([]string{}, request.Tools...)
	o.OnlyToolNames = &tools
	o.Arguments.SessionID = snapshot.SessionID
	o.Arguments.Resume = ""
	o.Arguments.ForkSession = false
	o.Arguments.Worktree = nil
	o.Arguments.Model = model
	o.Arguments.SystemPrompt = ""
	o.Arguments.HasCustomSystemPrompt = false
	o.Arguments.Tools = []string{strings.Join(request.Tools, ",")}
	o.Arguments.AllowedTools = nil
	o.Arguments.PermissionMode = request.PermissionContext.String("mode")
	o.Arguments.MaxTurns = math.MaxInt32
	// These are the parent QueryEngine's public print controls. The Source
	// Agent query does not inherit them, while its workload ALS does inherit.
	o.Arguments.MaxBudgetUSD, o.Arguments.TaskBudget = nil, nil
	o.Arguments.FallbackModel = ""
	o.Arguments.EnableAuthStatus = false
	if request.MaxTurns != nil && *request.MaxTurns <= math.MaxInt32 {
		o.Arguments.MaxTurns = int(*request.MaxTurns)
	}
	o.AgentEffort, o.AgentEffortOverridesEnv, e = m.Host.InferenceEffort(scope)
	if e != nil {
		return MainPrintOptions{}, e
	}
	if request.Effort != nil {
		o.AgentEffort = append(json.RawMessage(nil), request.Effort...)
		o.AgentEffortOverridesEnv = true
	}
	o.Input = strings.NewReader("")
	o.Output = io.Discard
	o.OnOutput = func([]byte) error { return nil }
	return o, nil
}

func (m *MainPrint) nativeAgentToolAllowed(name string) bool {
	d := m.options.DetachedAgent
	return d == nil || d.isBackgrounded == nil || !d.isBackgrounded() || d.backgroundToolNames[name]
}

func (m *MainPrint) nativeAgentToolFilter() func(string) bool {
	if d := m.options.DetachedAgent; d != nil && d.isBackgrounded != nil {
		return m.nativeAgentToolAllowed
	}
	return nil
}
