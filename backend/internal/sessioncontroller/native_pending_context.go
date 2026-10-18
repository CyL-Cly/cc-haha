package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// PendingRawContext carries the original two producers and their Controller
// provenance. Completion samples history/host fields after the outer aggregate;
// callers cannot supply formatted user context or substitute a completed value.
type PendingRawContext struct {
	controller    *Controller
	input         requestcontext.Context
	defaultPrompt *ownerasync.OwnerFuture[[]json.RawMessage]
	userContext   *ownerasync.OwnerFuture[*protocol.JSONObject]
}

func (controller *Controller) HasNativePendingContext() bool {
	return controller != nil && controller.options.NativeTools && controller.options.BeginNativeDefaultPrompt != nil && controller.options.BeginNativeUserContext != nil
}
func (controller *Controller) BeginRawContextOnOwner(scope *cooperative.Scope, input requestcontext.Context) (*PendingRawContext, error) {
	if !controller.HasNativePendingContext() || controller.options.Bootstrap == nil || !scope.BelongsTo(controller.options.Bootstrap.Owner()) || !controller.options.PublicPrint {
		return nil, cooperative.ErrRestricted
	}
	if !controller.active {
		return nil, errors.New("headless session is not active")
	}
	if input.History != nil {
		return nil, errors.New("headless request forbids alternate prepared history")
	}
	if err := controller.checkTools(input.Tools); err != nil {
		return nil, err
	}
	// Array evaluation starts default prompt first, then the shared user memo.
	defaultPrompt, err := controller.options.BeginNativeDefaultPrompt(scope, controller.registeredTools, input.Options.Model)
	if err != nil {
		return nil, err
	}
	user, err := controller.options.BeginNativeUserContext(scope)
	if err != nil {
		return nil, err
	}
	return &PendingRawContext{controller: controller, input: input, defaultPrompt: defaultPrompt, userContext: user}, nil
}
func (p *PendingRawContext) DefaultPrompt() *ownerasync.OwnerFuture[[]json.RawMessage] {
	return p.defaultPrompt
}
func (p *PendingRawContext) UserContext() *ownerasync.OwnerFuture[*protocol.JSONObject] {
	return p.userContext
}

func (p *PendingRawContext) CompleteOnOwner(scope *cooperative.Scope) (RawContext, error) {
	if p == nil || p.controller == nil || p.controller.options.Bootstrap == nil || !scope.BelongsTo(p.controller.options.Bootstrap.Owner()) {
		return RawContext{}, cooperative.ErrRestricted
	}
	select {
	case <-p.defaultPrompt.Done():
	default:
		return RawContext{}, cooperative.ErrRestricted
	}
	select {
	case <-p.userContext.Done():
	default:
		return RawContext{}, cooperative.ErrRestricted
	}
	prompt, err := p.defaultPrompt.Wait(context.Background())
	if err != nil {
		return RawContext{}, err
	}
	user, err := p.userContext.Wait(context.Background())
	if err != nil {
		return RawContext{}, err
	}
	if user == nil {
		return RawContext{}, cooperative.ErrRestricted
	}
	return p.controller.finishRawContext(p.input, prompt, user)
}

func (controller *Controller) finishRawContext(input requestcontext.Context, prompt []json.RawMessage, resolved *protocol.JSONObject) (RawContext, error) {
	if !controller.active {
		return RawContext{}, errors.New("headless session is not active")
	}
	if err := controller.checkTools(input.Tools); err != nil {
		return RawContext{}, err
	}
	if controller.options.ResolveSystemPrompt == nil && controller.options.ResolveSystemPromptForModel == nil {
		prompt = []json.RawMessage{controller.options.CustomSystemPrompt}
	}
	for _, section := range prompt {
		if _, err := protocol.DecodeJavaScriptJSONString(section); err != nil {
			return RawContext{}, capability("resolved system prompt section must be an original string")
		}
	}
	input.Host.SessionID = controller.SessionID()
	input.Options.QuerySource, input.Options.IsNonInteractiveSession = "sdk", true
	if controller.options.QuerySource != "" {
		input.Options.QuerySource = controller.options.QuerySource
	}
	if controller.options.NonInteractive != nil {
		input.Options.IsNonInteractiveSession = *controller.options.NonInteractive
	}
	input.SystemPrompt = prompt
	input.History = make([]protocol.JSONObject, 0, len(controller.messages))
	for _, message := range controller.messages {
		input.History = append(input.History, message.Clone())
	}
	user := resolved.Clone()
	if controller.options.AgentOmitClaudeMd {
		user.Delete("claudeMd")
	}
	return RawContext{Context: input, UserContext: user, owner: controller}, nil
}
