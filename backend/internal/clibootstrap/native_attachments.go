package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// Preserve getAttachmentMessages' native producer order and single-consumption
// state. The same callback serves initial input, tool rounds and Skill input.
func (m *MainPrint) nativeAttachments(scope *cooperative.Scope, history []queryloop.Message) ([]queryloop.Message, error) {
	return m.nativeAttachmentsForContext(scope, history, nil)
}

func (m *MainPrint) nativeContextAttachments(scope *cooperative.Scope, history []queryloop.Message, call *builtinwrite.CallContext) ([]queryloop.Message, error) {
	if call == nil || call.AgentID == "" {
		return m.nativeAttachments(scope, history)
	}
	if !call.Scope.BelongsTo(m.Owner) || !scope.BelongsTo(m.Owner) || call.PermissionContext == nil {
		return nil, cooperative.ErrRestricted
	}
	return m.nativeAttachmentsForContext(scope, history, call)
}

func (m *MainPrint) nativeAttachmentsForContext(scope *cooperative.Scope, history []queryloop.Message, call *builtinwrite.CallContext) ([]queryloop.Message, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	agentID := m.options.AgentID
	var queued []protocol.JSONObject
	var e error
	if call == nil {
		queued, e = m.nativeAgentQueuedAttachments(scope)
	} else {
		agentID = call.AgentID
		if d := m.options.DetachedAgent; d != nil && d.preset != nil {
			queued, e = nativeAgentOwnerCall(m, scope, d.preset.parent, func(current *cooperative.Scope) ([]protocol.JSONObject, error) {
				return d.preset.parent.Controller.TakeNativeQueuedAttachmentsOnOwner(current, agentID)
			})
		} else {
			queued, e = m.Controller.TakeNativeQueuedAttachmentsOnOwner(scope, agentID)
		}
	}
	if e != nil {
		return nil, e
	}
	commandAttachments := []queryloop.Message{}
	for _, item := range queued {
		commandAttachments = append(commandAttachments, queryloop.Message{Record: m.factory.CreateAttachment(rawMain(item))})
	}
	if m.options.Native != nil && (skillTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"]) || skillTruthy(m.options.Native.Environment["CLAUDE_CODE_SIMPLE"])) {
		// These callers supply Source's empty queuedCommands; the early gate
		// must not consume dynamic directories, sent skills or plan exit flags.
		return commandAttachments, nil
	}
	delta, e := m.nativeDeferredToolsAttachments(scope, history, call)
	if e != nil {
		return nil, e
	}
	skills := []queryloop.Message{}
	if m.skillCommands != nil {
		var err error
		skills, err = m.nativeSkillAttachments(scope, history, call)
		if err != nil {
			return nil, err
		}
	}
	ultra, err := m.nativeUltracodeAttachments(scope, history, agentID)
	if err != nil {
		return nil, err
	}
	skills = append(skills, ultra...)
	plans, err := m.nativePlanAttachments(scope, history, call)
	if err != nil {
		return nil, err
	}
	mailbox, err := m.nativeTeamMailboxAttachments(scope, agentID)
	if err != nil {
		return nil, err
	}
	active, e := m.Controller.NativeAutoModeActive(scope)
	if e != nil {
		return nil, e
	}
	auto, e := m.nativeAutoModeAttachments(scope, history, active, call)
	if e != nil {
		return nil, e
	}
	plans = append(plans, auto...)
	autoExit, e := m.state.TakeAutoModeExitAttachment(scope, active)
	if e != nil {
		return nil, e
	}
	if autoExit {
		plans = append(plans, queryloop.Message{Record: m.factory.CreateAttachment(rawMain(protocol.NewJSONObject(fieldMain("type", "auto_mode_exit"))))})
	}
	plans = append(plans, mailbox...)
	if m.nativeTools != nil {
		if err = m.nativeTools.PollBackgroundTasks(scope); err != nil {
			return nil, err
		}
	}
	pending, err := builtintools.DrainAgentPendingAttachments(m.nativeBackgroundBindings(), scope, agentID)
	if err != nil {
		return nil, err
	}
	for _, item := range pending {
		plans = append(plans, queryloop.Message{Record: m.factory.CreateAttachment(rawMain(item))})
	}
	asyncHooks, err := m.nativeAsyncHookAttachments(scope, agentID)
	if err != nil {
		return nil, err
	}
	plans = append(plans, asyncHooks...)
	return append(append(append(commandAttachments, delta...), skills...), plans...), nil
}
