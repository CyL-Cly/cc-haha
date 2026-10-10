package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *MainPrint) nativeAsyncHookParent() *MainPrint {
	if d := m.options.DetachedAgent; d != nil {
		if d.authParent != nil {
			return d.authParent
		}
		if d.preset != nil {
			return d.preset.parent
		}
	}
	return nil
}
func (m *MainPrint) initializeNativeAsyncHooks() error {
	if !m.options.Native.NativeProcess {
		return nil
	}
	if parent := m.nativeAsyncHookParent(); parent != nil && parent.asyncHooks != nil {
		m.asyncHooks = parent.asyncHooks
		m.asyncHooksBorrowed = true
		return nil
	}
	var err error
	m.asyncHooks, err = queryhooks.NewAsyncRegistry(queryhooks.AsyncRegistryOptions{Owner: m.Owner, NowMillis: m.options.Native.NowMillis, Invalidate: m.hookEnvironment.Invalidate, Event: func(scope *cooperative.Scope, h queryhooks.AsyncHook, out queryhooks.CommandOutput, kind string) error {
		return m.nativeHookSDKEvent(queryhooks.Context{Scope: scope, HookName: h.HookName}, kind, h.HookID, h.HookEvent, out, "")
	}, Notify: func(scope *cooperative.Scope, text string) error {
		return m.Controller.EnqueueTaskNotificationOnOwner(scope, "", text)
	}})
	return err
}
func (m *MainPrint) registerNativeAsyncHook(call queryhooks.Context, hook protocol.JSONObject, out queryhooks.CommandOutput) error {
	if !out.Backgrounded {
		return nil
	}
	if m.asyncHooks == nil || out.AsyncProcess == nil {
		return mainCap("actual asynchronous Hook process registry")
	}
	value := queryhooks.AsyncHook{ProcessID: out.AsyncProcessID, HookID: call.HookID, HookName: call.HookName, HookEvent: call.HookEvent, Command: hook.String("command"), Timeout: out.AsyncTimeout, Process: out.AsyncProcess, Rewake: out.AsyncRewake}
	if m.asyncHooksBorrowed {
		return bridgeAgentVoid(m, m.nativeAsyncHookParent(), call.Scope, func(scope *cooperative.Scope) error { return m.asyncHooks.Register(scope, value) })
	}
	return m.asyncHooks.Register(call.Scope, value)
}
func (m *MainPrint) nativeAsyncHookAttachments(scope *cooperative.Scope, agentID string) ([]queryloop.Message, error) {
	if agentID != "" || m.asyncHooks == nil || m.asyncHooksBorrowed {
		return []queryloop.Message{}, nil
	}
	responses, err := m.asyncHooks.Responses(scope)
	if err != nil {
		return nil, err
	}
	out := []queryloop.Message{}
	for _, response := range responses {
		out = append(out, queryloop.Message{Record: m.factory.CreateAttachment(rawMain(queryhooks.AsyncAttachment(response)))})
	}
	return out, nil
}
func (m *MainPrint) closeNativeAsyncHooks(ctx context.Context) error {
	if m.asyncHooks == nil || m.asyncHooksBorrowed {
		return nil
	}
	return m.asyncHooks.Close(ctx)
}
