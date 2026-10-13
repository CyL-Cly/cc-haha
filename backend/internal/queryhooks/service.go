// Package queryhooks implements the original empty-hook settings/registry
// lifecycle. Active hook execution is a typed capability, never a noop host.
package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

const EmptyExternalProfile = "external-empty-hook-resolver-v1"
const ResolvedSettingsProfile = "shared-resolved-settings-hook-resolver-v1"

type CapabilityError struct{ Boundary string }

func (failure *CapabilityError) Error() string {
	return "query hooks capability is not migrated: " + failure.Boundary
}
func unsupported(boundary string) error { return &CapabilityError{boundary} }

type Options struct {
	Callbacks *CallbackBindings
	HTTP      *HTTPBindings
	Prompts   *PromptBindings
	Agents    *AgentBindings
	Commands  *CommandBindings
	// MergedSettings is the actual shared startup getInitialSettings producer.
	// When absent, older finite resolver callers retain their original file-source profile.
	MergedSettings        func() (*protocol.JSONObject, error)
	PolicySettings        func() (*protocol.JSONObject, error)
	PluginHooks           func() ([]protocol.JSONObject, error)
	Profile               string
	Settings              *clisettings.Service
	SettingsState         *clisettings.State
	OriginalEnv           map[string]string
	MainSessionID         string
	CurrentSessionID      func() string
	SessionStartMetadata  func(*cooperative.Scope) (json.RawMessage, json.RawMessage, error)
	ForceSyncSessionStart bool
	SessionStartBase      func(*cooperative.Scope, string) (protocol.JSONObject, error)
	UpdateWatchPaths      func(*cooperative.Scope, []json.RawMessage) error
	// The actual source policy/flag/plugin capabilities must be resolved by the
	// owning startup. This slice admits only their explicit empty external state.
	PolicyAbsent, FlagAbsent, PluginBaseAbsent bool
	UUID                                       func() string
}

// Context borrows the real outer history/system/context objects. None is a
// Source-prepared model request, and accepted live envelopes remain live.
type Context struct {
	ForceSyncExecution                                  bool
	PromptToolNames                                     *[]string
	HookIndex                                           *int
	HookID, HookEvent, HookName                         string
	InputFields                                         protocol.JSONObject
	Scope                                               *cooperative.Scope
	StopHookActive                                      bool
	History                                             []queryloop.Message
	Assistants                                          []*streamhistory.Envelope
	SystemPrompt                                        any
	UserContext, SystemContext                          map[string]string
	ToolUseContext                                      any
	QuerySource, AgentID                                string
	Interactive, TrustAccepted                          bool
	WorktreeWrapper                                     bool
	ComputerUseInactive, GoalInactive, TeammateInactive bool
	CleanupComputerUse                                  func(context.Context) error
	TeammateStop                                        func(context.Context, Context, queryloop.Emit) (queryloop.StopDecision, error)
	StartAutoDream                                      func(CacheSafeParams) error
}
type CacheSafeParams struct {
	Context  Context
	Messages []queryloop.Message
}
type Task struct{ done chan struct{} }

func (task *Task) Done() <-chan struct{} { return task.done }
func completed() *Task                   { done := make(chan struct{}); close(done); return &Task{done} }

type Service struct {
	mu                 sync.Mutex
	options            Options
	env                map[string]string
	resolver           resolverState
	registry           registryState
	saved              *CacheSafeParams
	startupResults     map[string]SessionStartResult
	initialUserMessage json.RawMessage
}

func New(options Options) (*Service, error) {
	if options.Profile == ResolvedSettingsProfile {
		if options.MergedSettings == nil || !options.PluginBaseAbsent && options.PluginHooks == nil {
			return nil, unsupported("shared native settings/plugin resolver")
		}
	} else if options.Profile != EmptyExternalProfile || !options.PolicyAbsent || !options.FlagAbsent || !options.PluginBaseAbsent {
		return nil, unsupported("original policy/flag/plugin profile")
	}
	if options.Settings == nil || options.SettingsState == nil || options.UUID == nil || options.MainSessionID == "" {
		return nil, errors.New("query hooks require settings service/state, main session ID and shared UUID")
	}
	if c := options.Commands; c != nil {
		if c.Owner == nil || c.Factory == nil || c.Timestamp == nil || c.NowMillis == nil || c.Input == nil || c.Run == nil {
			return nil, errors.New("native hook commands require actual owner, factory, clock, input and process producers")
		}
		if c.Store == nil {
			c.Store = &CommandStore{events: map[string]map[string][]commandMatcher{}}
		}
	}
	if h := options.HTTP; h != nil && (h.Owner == nil || h.Run == nil || options.Commands == nil || options.Commands.Owner != h.Owner) {
		return nil, errors.New("native HTTP hooks require the shared command owner and actual transport producer")
	}
	if a := options.Agents; a != nil && (a.Owner == nil || a.Transcript == nil || a.Model == nil || a.Query == nil || a.Clear == nil || options.Commands == nil || options.Commands.Owner != a.Owner) {
		return nil, errors.New("native Agent hooks require actual shared owner, transcript, model, query and session cleanup producers")
	}
	env, err := originalEnvironment(options.OriginalEnv)
	if err != nil {
		return nil, err
	}
	return &Service{options: options, env: env, registry: registryState{sessions: map[string]*sessionStore{}}}, nil
}
func originalEnvironment(input map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for key, value := range input {
		if key != "CLAUDE_CODE_SIMPLE" {
			return nil, unsupported("original environment " + key)
		}
		out[key] = value
	}
	return out, nil
}
func (service *Service) SetOriginalEnvironment(input map[string]string) error {
	env, err := originalEnvironment(input)
	if err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.env = env
	return nil
}
func (service *Service) simple() bool {
	switch strings.ToLower(strings.TrimFunc(service.env["CLAUDE_CODE_SIMPLE"], javaScriptWhitespace)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// StartPostSampling enters synchronously. Empty programmatic registry completes
// its real loop immediately; active callbacks require their separately ported
// service and cannot be turned into a goroutine noop.
func (service *Service) StartPostSampling(call Context) (*Task, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.registry.programmatic != 0 {
		return nil, unsupported("programmatic PostSampling execution")
	}
	return completed(), nil
}

// StartStopFailure preserves the main-SID existence gate before SIMPLE/trust.
// It does not inherit the cancelled model context: Source's outside-REPL failure
// executor has its own timeout, and this supported empty path starts no work.
func (service *Service) StartStopFailure(call Context, last *streamhistory.Envelope) (*Task, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if last == nil {
		return nil, errors.New("query hooks require the actual terminal assistant")
	}
	has, err := service.exists("StopFailure", service.mainSessionID())
	if err != nil {
		return nil, err
	}
	if !has {
		return completed(), nil
	}
	if service.simple() || call.Interactive && !call.TrustAccepted {
		return completed(), nil
	}
	active := service.active("StopFailure", service.mainSessionID())
	if active {
		return nil, unsupported("StopFailure execution")
	}
	return completed(), nil
}

// StopHooks consumes the original normal-stop empty resolver. Saving cache-safe
// references and resource checks precede the existence/skip gate even in bare
// mode. No Source hook decision or rendered attachment is accepted here.
func (service *Service) StopHooks(ctx context.Context, call Context, emit queryloop.Emit) (decision queryloop.StopDecision, failure error) {
	if ctx == nil || emit == nil {
		return queryloop.StopDecision{}, errors.New("query hooks require caller context and controlled consumer")
	}
	if !call.GoalInactive && (service.options.Prompts == nil || !service.HasNativePromptBindings(service.options.Prompts.Owner)) || !call.TeammateInactive && call.TeammateStop == nil || !call.ComputerUseInactive && call.CleanupComputerUse == nil {
		return queryloop.StopDecision{}, unsupported("normal-stop computer-use/goal/team host")
	}
	defer func() {
		if failure == nil && !decision.PreventContinuation && len(decision.BlockingMessages) == 0 && !call.TeammateInactive {
			decision, failure = call.TeammateStop(ctx, call, emit)
		}
	}()
	service.mu.Lock()
	if call.QuerySource == "sdk" || call.QuerySource == "repl_main_thread" {
		messages := append([]queryloop.Message(nil), call.History...)
		for _, assistant := range call.Assistants {
			messages = append(messages, queryloop.Message{Assistant: assistant})
		}
		service.saved = &CacheSafeParams{call, messages}
	}
	service.mu.Unlock()
	// Source saves the actual context before cleanup, then reads Stop hooks
	if call.AgentID == "" && call.StartAutoDream != nil {
		messages := append([]queryloop.Message(nil), call.History...)
		for _, assistant := range call.Assistants {
			messages = append(messages, queryloop.Message{Assistant: assistant})
		}
		if err := call.StartAutoDream(CacheSafeParams{call, messages}); err != nil {
			return queryloop.StopDecision{}, err
		}
	}
	// afterward. An owner-native await must never retain the resolver mutex.
	if !call.ComputerUseInactive && call.AgentID == "" {
		_ = call.CleanupComputerUse(ctx)
	}
	if call.AgentID == "" && service.options.Prompts != nil && service.options.Prompts.RestoreGoal != nil {
		var err error
		call.GoalInactive, err = service.options.Prompts.RestoreGoal(ctx, call)
		if err != nil {
			return queryloop.StopDecision{}, err
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	event, sid := "Stop", service.mainSessionID()
	if call.AgentID != "" {
		event, sid = "SubagentStop", call.AgentID
	}
	has, err := service.exists(event, sid)
	if err != nil {
		return queryloop.StopDecision{}, err
	}
	if !has {
		return queryloop.StopDecision{BlockingMessages: []queryloop.Message{}}, nil
	}
	// Source allocates executeStopHooks' toolUseID before executeHooks checks
	// SIMPLE/trust or discovers that an empty matcher resolves no callbacks.
	toolUseID := service.options.UUID()
	if service.options.Commands != nil && service.resolver.disabled {
		return queryloop.StopDecision{BlockingMessages: []queryloop.Message{}}, nil
	}
	if service.simple() || call.Interactive && !call.TrustAccepted {
		return queryloop.StopDecision{BlockingMessages: []queryloop.Message{}}, nil
	}
	commands, err := service.configuredNativeCommands(event, sid)
	if err != nil {
		return queryloop.StopDecision{}, err
	}
	if len(commands) > 0 && !service.resolver.disabled {
		if service.activeCallbacks(event, sid) {
			return queryloop.StopDecision{}, unsupported("mixed native command and callback hook execution")
		}
		service.mu.Unlock()
		defer service.mu.Lock()
		return service.executeNativeCommandStop(ctx, call, event, toolUseID, commands, emit)
	}
	active := service.active(event, sid)
	if active {
		return queryloop.StopDecision{}, unsupported(event + " execution")
	}
	return queryloop.StopDecision{BlockingMessages: []queryloop.Message{}}, nil
}
func (service *Service) CacheSafeParams() *CacheSafeParams {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.saved
}
