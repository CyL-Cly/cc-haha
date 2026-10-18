package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"os"
)

// RawContext is the original ASK context and an independent memo snapshot.
// It contains no prefix envelope, request params or prepared request. The
// private provenance prevents accepting a serialized/foreign raw context.
type RawContext struct {
	Context     requestcontext.Context
	UserContext protocol.JSONObject
	owner       *Controller
}

// ResolveRawContext matches fetchSystemPromptParts' no-signal wait. Caller
// abort does not cancel or shortcut a shared original memory producer. The
// query host checks its actual abort/blocking policies after this completes.
func (controller *Controller) ResolveRawContext(input requestcontext.Context) (RawContext, error) {
	return controller.resolveRawContext(context.Background(), input, nil)
}

// ResolveContext is the existing finite compatibility adapter. Its ctx can
// stop this waiter while the shared memo continues; new Source hosts must use
// ResolveRawContext instead. It never calls requestcontext.Prepare.
func (controller *Controller) ResolveContext(ctx context.Context, input requestcontext.Context) (requestcontext.Context, error) {
	resolved, err := controller.resolveRawContext(ctx, input, nil)
	if err != nil {
		return requestcontext.Context{}, err
	}
	return controller.ProjectUserContext(resolved)
}

// ResolveRawContextOnOwner is the actual NativeTools ASK prefix producer.
// Only the factory-owned bootstrap/current scope may observe the shared memo.
func (controller *Controller) ResolveRawContextOnOwner(scope *cooperative.Scope, input requestcontext.Context) (RawContext, error) {
	if controller.options.Bootstrap == nil || !scope.BelongsTo(controller.options.Bootstrap.Owner()) || !controller.options.PublicPrint || !controller.options.NativeTools {
		return RawContext{}, cooperative.ErrRestricted
	}
	return controller.resolveRawContext(context.Background(), input, scope)
}

func (controller *Controller) resolveRawContext(ctx context.Context, input requestcontext.Context, scope *cooperative.Scope) (RawContext, error) {
	if !controller.active {
		return RawContext{}, errors.New("headless session is not active")
	}
	if input.History != nil {
		return RawContext{}, errors.New("headless request forbids alternate prepared history")
	}
	if err := controller.checkTools(input.Tools); err != nil {
		return RawContext{}, err
	}
	if controller.options.UserContext == nil {
		return RawContext{}, capability("missing original user context resolver")
	}
	if controller.legacyEnvironment != nil {
		for _, property := range controller.legacyEnvironment.Snapshot().OwnProperties() {
			var key string
			_ = json.Unmarshal(property.NameJSON, &key)
			if _, exists := controller.options.ResumeOptions.Environment[key]; !exists {
				if err := controller.legacyEnvironment.SetProperty(property.NameJSON, nil); err != nil {
					return RawContext{}, err
				}
			}
		}
		for key, value := range controller.options.ResumeOptions.Environment {
			if err := controller.legacyEnvironment.SetProperty(quote(key), quote(value)); err != nil {
				return RawContext{}, err
			}
		}
	}
	future := controller.options.UserContext.GetUserContext()
	var resolved *protocol.JSONObject
	var err error
	if scope == nil {
		resolved, err = future.Await(ctx)
	} else {
		resolved, err = future.AwaitOnOwner(scope)
	}
	if err != nil {
		return RawContext{}, err
	}
	input.Host.SessionID = controller.SessionID()
	input.Options.QuerySource = "sdk"
	input.Options.IsNonInteractiveSession = true
	if controller.options.QuerySource != "" {
		input.Options.QuerySource = controller.options.QuerySource
	}
	if controller.options.NonInteractive != nil {
		input.Options.IsNonInteractiveSession = *controller.options.NonInteractive
	}
	input.SystemPrompt = []json.RawMessage{controller.options.CustomSystemPrompt}
	if controller.options.ResolveSystemPrompt != nil || controller.options.ResolveSystemPromptForModel != nil {
		if controller.options.ResolveSystemPromptForModel != nil {
			input.SystemPrompt, err = controller.options.ResolveSystemPromptForModel(ctx, controller.registeredTools, input.Options.Model)
		} else {
			input.SystemPrompt, err = controller.options.ResolveSystemPrompt(ctx, controller.registeredTools)
		}
		if err != nil {
			return RawContext{}, err
		}
		for _, section := range input.SystemPrompt {
			if _, err = protocol.DecodeJavaScriptJSONString(section); err != nil {
				return RawContext{}, capability("resolved system prompt section must be an original string")
			}
		}
	}
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

// GoHistoryProjection is local preflight business over original message
// snapshots. It must not deserialize Source prepared history/request data.
// This interface alone is not proof that a caller migrated its policies.
type GoHistoryProjection func([]protocol.JSONObject) ([]protocol.JSONObject, error)

// ProjectUserContextWithHistory applies the admitted Go preflight projection
// before allocating a user prefix. A missing policy is never an identity
// fallback; the compatibility adapter below is explicitly separate.
func (controller *Controller) ProjectUserContextWithHistory(resolved RawContext, projection GoHistoryProjection) (requestcontext.Context, error) {
	if !controller.active {
		return requestcontext.Context{}, errors.New("headless session is not active")
	}
	if resolved.owner != controller {
		return requestcontext.Context{}, capability("foreign or fabricated raw context")
	}
	if projection == nil {
		return requestcontext.Context{}, capability("missing Go-owned preflight history projection")
	}
	if err := controller.checkTools(resolved.Context.Tools); err != nil {
		return requestcontext.Context{}, err
	}
	original := make([]protocol.JSONObject, 0, len(resolved.Context.History))
	for _, message := range resolved.Context.History {
		original = append(original, message.Clone())
	}
	history, err := projection(original)
	if err != nil {
		return requestcontext.Context{}, err
	}
	resolved.Context.History = history
	return controller.ProjectUserContext(resolved)
}

// ProjectUserContext is the late model-call projection after the host has
// evaluated its blocking policy. Source can still allocate this prefix for
// an already-aborted call; this method never shortcuts on caller cancellation.
// It uses the same real session factory and clock, and never persists the
// meta envelope.
func (controller *Controller) ProjectUserContext(resolved RawContext) (requestcontext.Context, error) {
	if !controller.active {
		return requestcontext.Context{}, errors.New("headless session is not active")
	}
	if resolved.owner != controller {
		return requestcontext.Context{}, capability("foreign or fabricated raw context")
	}
	input := resolved.Context
	if err := controller.checkTools(input.Tools); err != nil {
		return requestcontext.Context{}, err
	}
	content, err := usercontext.RenderPrefix(resolved.UserContext)
	if err != nil {
		return requestcontext.Context{}, err
	}
	input.History = make([]protocol.JSONObject, 0, len(resolved.Context.History)+1)
	if content != nil {
		meta := orderedUser(controller.options.Factory.CreateUser(storage.Object{"content": content, "isMeta": raw(true)}))
		input.History = append(input.History, meta)
	}
	for _, message := range resolved.Context.History {
		input.History = append(input.History, message.Clone())
	}
	return input, nil
}
func legacyContext(options Options) (*usercontext.Service, *clistartupenv.Environment, error) {
	state := options.ResumeOptions.SettingsState
	if state == nil {
		return nil, nil, capability("missing CLI settings cache for context")
	}
	for _, path := range options.ResumeOptions.PolicyPaths {
		if options.ResumeOptions.Files == nil {
			return nil, nil, capability("missing original policy filesystem")
		}
		data, readErr := options.ResumeOptions.Files.ReadFile(path)
		if os.IsNotExist(readErr) {
			continue
		}
		var policy protocol.JSONObject
		if readErr != nil || json.Unmarshal(data, &policy) != nil || !policy.IsObject() || len(policy.OwnProperties()) != 0 {
			return nil, nil, capability("managed policy presence/error requires original context host")
		}
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: options.ResumeOptions.ConfigDir, OriginalCWD: options.ResumeOptions.OriginalCWD, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return float64(options.Now().UnixMilli()) }, PID: os.Getpid()})
	if err != nil {
		return nil, nil, err
	}
	original := protocol.NewJSONObject()
	for key, value := range options.ResumeOptions.Environment {
		original.Set(key, quote(value))
	}
	environment, err := clistartupenv.NewEnvironment(original)
	if err != nil {
		return nil, nil, err
	}
	// Old admitted Controller profile is --tools empty/custom prompt/bare. Its
	// explicit no-policy paths already belong to ResumeOptions, not ambient MDM.
	service, err := usercontext.New(usercontext.Options{Environment: environment, Settings: settings, SettingsState: state, Now: options.Now, NowMillis: options.NowMillis, Diagnostics: options.Diagnostics, Profile: usercontext.ExternalProfile})
	return service, environment, err
}
