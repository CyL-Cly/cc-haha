// Package queryloop ports the ordinary successful tool-round consumer in
// src/query.ts. Attempt/recovery policy, compaction, permissions, tools and
// hooks are explicit host capabilities, never permissive defaults.
package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionactivity"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

const SuccessfulToolRound = "ordinary-successful-tool-round"

var ErrUnsupported = errors.New("query loop input requires an unimplemented attempt policy")

type ScopeError struct {
	Boundary string
	Cause    error
}

func (failure *ScopeError) Error() string {
	return "query loop unsupported " + failure.Boundary + ": " + failure.Cause.Error()
}
func (failure *ScopeError) Unwrap() error            { return failure.Cause }
func (failure *ScopeError) Is(target error) bool     { return target == ErrUnsupported }
func unsupported(boundary string, cause error) error { return &ScopeError{boundary, cause} }

// Message retains live provider envelopes. Snapshot only at request, output or
// persistence boundaries; serializing at block_stop would freeze later usage.
type Message struct {
	Record    protocol.JSONObject
	Assistant *streamhistory.Envelope
}

func (message Message) MarshalJSON() ([]byte, error) {
	if message.Assistant != nil {
		return message.Assistant.MarshalJSON()
	}
	raw, err := message.Record.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}
func (message Message) Type() string {
	if message.Assistant != nil {
		return "assistant"
	}
	return message.Record.String("type")
}
func Snapshot(history []Message) ([]json.RawMessage, error) {
	output := make([]json.RawMessage, 0, len(history))
	for _, message := range history {
		raw, err := message.MarshalJSON()
		if err != nil {
			return nil, err
		}
		output = append(output, raw)
	}
	return output, nil
}

type Tracking struct {
	ChainID string
	Depth   int
}
type Round struct {
	Number      int
	History     []Message
	Tracking    Tracking
	ToolContext any
}
type Invocation struct {
	Block     *streamhistory.Block
	Assistant *streamhistory.Envelope
}
type Backfill func(input *protocol.JSONObject) error

// Yield has exactly one value. Emit is synchronous: the caller may suspend
// here like Source's generator, before internal history or tools advance.
type Yield struct {
	ModelFallbackWarning bool
	RequestStart         bool
	APIRetryNotice       bool
	APIRetryError        *provider.SDKStreamError
	Stream               *streamhistory.Update
	Message              *Message
}
type Emit func(Yield) error
type ControlledEmit func(Yield) (requestretry.NoticeAction, error)

// SubmitInterrupt is the literal queued-submit cause, distinct from a user
// control interrupt. Wrapped/other causes must not silently become this cause.
var SubmitInterrupt = errors.New("query submit interrupt")

type StopDecision struct {
	PreventContinuation       bool
	BlockingMessages          []Message
	AllowBlockingContinuation bool
}

// PreflightTerminal is the reached pre-model blocking branch. It is separate
// from provider/modelyield errors and never accepts a prepared request.
type PreflightTerminal struct {
	Message Message
	Reason  string
}

// PreflightResult separates the continuation view from the host's earlier
// ToolContext view. The concrete resolver derives these from original state.
type PreflightResult struct {
	History     []Message
	ToolContext any
	Terminal    *PreflightTerminal
	// CurrentModel is the actual runtime selection after preflight business.
	// An absent selection retains the original exact-main-model contract.
	CurrentModel string
}

type Host struct {
	OnNativeQuota        func(*cooperative.Scope, *provider.BunHeaders, error) error
	ModelFallbackChanged func(context.Context, string) error
	ModelDisplayName     func(string) (string, error)
	Activity             *sessionactivity.Tracker
	// Preflight resolves existing host context/compaction decisions. This slice
	// does not implement those policies or invent an identity fallback.
	Preflight func(context.Context, Round) ([]Message, error)
	// ResolvePreflight is mutually exclusive with the existing adapter. It
	// must run the actual original policies, not an identity fallback.
	ResolvePreflight    func(context.Context, Round) (PreflightResult, error)
	ResolveModelInput   func(context.Context, Round) (requestretry.Input, error)
	RetryAPIError       func(error) // optional live Error observer; no serialized yield changes
	Request             requestretry.Dependencies
	Session             *modelaccounting.Session
	SuccessDuration     *modelaccounting.SuccessDuration
	StreamClock         *modelyield.ProducerClock
	StreamPolicy        *modelyield.StreamErrorPolicy
	AgentContext        *modelaccounting.AgentContextBinding
	LookupBackfill      func(toolName string) (Backfill, error)
	LookupBackfillJSON  func(json.RawMessage) (Backfill, error)
	ExecuteBatch        func(context.Context, Round, []Invocation, func(Message) error) (any, error)
	NormalizeToolResult func(Message) ([]Message, error)
	// Source starts post-sampling hooks without awaiting their completion.
	PostSampling          func(Round, []*streamhistory.Envelope) error
	StopFailure           func(Round, *streamhistory.Envelope) error
	AbortCleanup          func(context.Context, Round) error
	StopHooks             func(context.Context, Round, []*streamhistory.Envelope, Emit) (StopDecision, error)
	PauseAfterTools       func(context.Context, Round) (bool, error)
	AttachmentsAfterTools func(context.Context, Round) ([]Message, error)
	RefreshTools          func() error // Original optional ToolUseContext.options.refreshTools.
}
type Options struct {
	// NativeModelPresent distinguishes an actual Source string override ""
	// from a missing model in the finite host contract.
	NativeModelPresent     bool
	FallbackModel          string
	Profile                string
	StreamingToolExecution bool
	Model, QuerySource     string
	NonInteractive         bool
	MaxTurns               int
	UUID                   func() string
	Now                    func() time.Time
	InitialTracking        *Tracking
	ToolContext            any
}
type Outcome struct {
	Reason      string
	History     []Message
	ToolContext any
	Tracking    Tracking
}

func validate(ctx context.Context, options Options, host Host, emit ControlledEmit) error {
	if options.Profile != SuccessfulToolRound || options.StreamingToolExecution {
		return unsupported("resolved execution profile", ErrUnsupported)
	}
	if ctx == nil || options.Model == "" && !options.NativeModelPresent || options.QuerySource == "" || options.MaxTurns < 1 || options.UUID == nil || options.Now == nil || emit == nil || host.Request.Random == nil || host.Request.Sleep == nil || host.Request.Notice != nil {
		return errors.New("query loop requires explicit model, query source, turn limit, original request host, clock and single consumer")
	}
	if (host.Preflight == nil) == (host.ResolvePreflight == nil) || host.ResolveModelInput == nil || (host.LookupBackfill == nil && host.LookupBackfillJSON == nil) || host.ExecuteBatch == nil || host.NormalizeToolResult == nil || host.PostSampling == nil || host.StopFailure == nil || host.AbortCleanup == nil || host.StopHooks == nil || host.PauseAfterTools == nil {
		return errors.New("query loop requires explicit host capabilities")
	}
	return nil
}

// Run is the always-resume adapter of the same outer loop. It never installs
// another model opener/consumer or accepts a prepared request.
func Run(ctx context.Context, initial []Message, options Options, host Host, emit Emit) (Outcome, error) {
	if emit == nil {
		return RunControlled(ctx, initial, options, host, nil)
	}
	return RunControlled(ctx, initial, options, host, func(value Yield) (requestretry.NoticeAction, error) {
		return requestretry.Resume, emit(value)
	})
}

func RunControlled(ctx context.Context, initial []Message, options Options, host Host, consumer ControlledEmit) (result Outcome, failure error) {
	if err := validate(ctx, options, host, consumer); err != nil {
		return Outcome{}, err
	}
	history := append([]Message(nil), initial...)
	toolContext := options.ToolContext
	stopped := false
	emit := func(value Yield) error {
		action, err := consumer(value)
		if err != nil {
			return err
		}
		switch action {
		case requestretry.Resume:
			return nil
		case requestretry.Stop:
			stopped = true
			return requestretry.ErrConsumerStopped
		default:
			return unsupported("consumer action", ErrUnsupported)
		}
	}
	var tracking Tracking
	if options.InitialTracking != nil {
		tracking = *options.InitialTracking
	}
	defer func() {
		if stopped && failure == requestretry.ErrConsumerStopped {
			result = Outcome{"consumer_stopped", history, toolContext, tracking}
			failure = nil
		}
	}()
	for number := 1; ; number++ {
		if err := emit(Yield{RequestStart: true}); err != nil {
			return Outcome{}, err
		}
		if number == 1 && options.InitialTracking == nil {
			tracking.ChainID = options.UUID()
		} else {
			tracking.Depth++
		}
		round := Round{number, history, tracking, toolContext}
		currentModel := options.Model
		var resolved []Message
		var err error
		if host.ResolvePreflight == nil {
			resolved, err = host.Preflight(ctx, round)
		} else {
			var decision PreflightResult
			decision, err = host.ResolvePreflight(ctx, round)
			if err == nil {
				resolved = decision.History
				if decision.CurrentModel != "" {
					currentModel = decision.CurrentModel
				}
				toolContext = decision.ToolContext
				round.ToolContext = toolContext
				if decision.Terminal != nil {
					terminal := decision.Terminal
					if terminal.Reason != "blocking_limit" || terminal.Message.Assistant != nil || terminal.Message.Type() != "assistant" || string(terminal.Message.Record.Get("isApiErrorMessage")) != "true" || terminal.Message.Record.String("error") != "invalid_request" || terminal.Message.Record.String("businessErrorCode") != "prompt_too_long" {
						return Outcome{}, unsupported("pre-model terminal decision", ErrUnsupported)
					}
					if err := emit(Yield{Message: &terminal.Message}); err != nil {
						return Outcome{}, err
					}
					// Source returns at this resumed yield before model entry,
					// post-sampling, StopFailure, StopHooks or abort cleanup.
					// Its input history is unchanged; the host owns acceptance.
					return Outcome{terminal.Reason, history, toolContext, tracking}, nil
				}
			}
		}
		if err != nil {
			return Outcome{}, err
		}
		round.History = resolved
		input, err := host.ResolveModelInput(ctx, round)
		if err != nil {
			return Outcome{}, err
		}
		if input.Context.Options.Model != currentModel || input.Context.Options.QuerySource != options.QuerySource || input.Context.Options.IsNonInteractiveSession != options.NonInteractive {
			return Outcome{}, unsupported("original model input/outer options mismatch", ErrUnsupported)
		}
		if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
			// Source query passes this iteration's already-created chain/depth
			// into callModel. Logging never allocates a second chain UUID.
			input.Context.Options.QueryTracking, err = json.Marshal(struct {
				ChainID string `json:"chainId"`
				Depth   int    `json:"depth"`
			}{round.Tracking.ChainID, round.Tracking.Depth})
			if err != nil {
				return Outcome{}, err
			}
		}
		assistants, uses, model, err := consumeWithModelFallback(ctx, input, options, host, emit, func() bool { return stopped })
		if err != nil {
			return Outcome{}, err
		}
		next := append([]Message(nil), resolved...)
		for _, assistant := range assistants {
			next = append(next, Message{Assistant: assistant})
		}
		if model.Reason == "consumer_stopped" {
			return Outcome{"consumer_stopped", next, toolContext, tracking}, nil
		}
		history = next
		if len(assistants) > 0 {
			// Enter synchronously; the host owns background completion.
			if err := host.PostSampling(round, assistants); err != nil {
				return Outcome{}, err
			}
		}
		if ctx.Err() != nil {
			if len(uses) != 0 {
				return Outcome{}, unsupported("pending tool cancellation/results", ctx.Err())
			}
			if err := host.AbortCleanup(ctx, round); err != nil {
				return Outcome{}, err
			}
			if context.Cause(ctx) != SubmitInterrupt {
				factory, err := messages.NewFactory(options.UUID, options.Now)
				if err != nil {
					return Outcome{}, err
				}
				message := Message{Record: messages.OrderedUser(factory.CreateUserInterruption(false))}
				if err := emit(Yield{Message: &message}); err != nil {
					return Outcome{}, err
				}
				next = append(next, message)
			}
			return Outcome{"aborted_streaming", next, toolContext, tracking}, nil
		}
		// Source's only ordinary follow-up signal is tool_use presence, even when
		// stop_reason is end_turn. Stop hooks execute only on the no-tool branch.
		if len(uses) == 0 {
			if len(assistants) > 0 {
				last := assistants[len(assistants)-1]
				if string(last.Record.Get("isApiErrorMessage")) == "true" {
					if err := host.StopFailure(round, last); err != nil {
						return Outcome{}, err
					}
					return Outcome{"completed", next, toolContext, tracking}, nil
				}
			}
			decision, err := host.StopHooks(ctx, round, assistants, emit)
			if err != nil {
				return Outcome{}, err
			}
			if !decision.PreventContinuation && len(decision.BlockingMessages) > 0 {
				if !decision.AllowBlockingContinuation {
					return Outcome{}, unsupported("blocking stop-hook continuation", ErrUnsupported)
				}
				history = append(next, decision.BlockingMessages...)
				continue
			}
			reason := "completed"
			if decision.PreventContinuation {
				reason = "stop_hook_prevented"
			}
			return Outcome{reason, next, toolContext, tracking}, nil
		}
		updated, err := host.ExecuteBatch(ctx, round, uses, func(message Message) error {
			if err := emit(Yield{Message: &message}); err != nil {
				return err
			}
			if message.Type() == "attachment" {
				next = append(next, message)
				history = next
				return nil
			}
			normalized, err := host.NormalizeToolResult(message)
			if err != nil {
				return err
			}
			for _, item := range normalized {
				if item.Type() == "user" {
					next = append(next, item)
					history = next
				}
			}
			return nil
		})
		if err != nil {
			return Outcome{}, err
		}
		toolContext = updated
		round.ToolContext = updated
		pause, err := host.PauseAfterTools(ctx, round)
		if err != nil {
			return Outcome{}, err
		}
		if pause {
			return Outcome{"completed", next, toolContext, tracking}, nil
		}
		if host.AttachmentsAfterTools != nil {
			round.History = append([]Message(nil), next...)
			attached, err := host.AttachmentsAfterTools(ctx, round)
			if err != nil {
				return Outcome{}, err
			}
			for _, message := range attached {
				if message.Type() != "attachment" {
					return Outcome{}, unsupported("post-tool attachment producer", ErrUnsupported)
				}
				if err := emit(Yield{Message: &message}); err != nil {
					return Outcome{}, err
				}
				next = append(next, message)
				history = next
			}
		}
		if host.RefreshTools != nil {
			if err := host.RefreshTools(); err != nil {
				return Outcome{}, err
			}
		}
		if ctx.Err() != nil {
			if err := host.AbortCleanup(ctx, round); err != nil {
				return Outcome{}, err
			}
			if context.Cause(ctx) != SubmitInterrupt {
				factory, err := messages.NewFactory(options.UUID, options.Now)
				if err != nil {
					return Outcome{}, err
				}
				message := Message{Record: messages.OrderedUser(factory.CreateUserInterruption(true))}
				if err := emit(Yield{Message: &message}); err != nil {
					return Outcome{}, err
				}
				next = append(next, message)
				history = next
			}
			if number >= options.MaxTurns {
				message := maxTurnsMessage(options, number+1)
				if err := emit(Yield{Message: &message}); err != nil {
					return Outcome{}, err
				}
			}
			return Outcome{"aborted_tools", next, toolContext, tracking}, nil
		}
		if number >= options.MaxTurns {
			message := maxTurnsMessage(options, number+1)
			if err := emit(Yield{Message: &message}); err != nil {
				return Outcome{}, err
			}
			return Outcome{"max_turns", next, toolContext, tracking}, nil
		}
		history = next
	}
}

// consume delegates physical creation, retries and response assembly to the one
// shared model adapter. Only outer acceptance/backfill/tool collection lives here.
func consume(ctx context.Context, input requestretry.Input, options Options, host Host, emit Emit, stopped func() bool) ([]*streamhistory.Envelope, []Invocation, modelyield.Outcome, error) {
	assistants := []*streamhistory.Envelope{}
	uses := []Invocation{}
	request := host.Request
	// All query/model/factory allocations share the caller's one source.
	request.UUID, request.Now = options.UUID, options.Now
	outcome, err := modelyield.Run(ctx, input, modelyield.Dependencies{OnNativeQuota: host.OnNativeQuota, Activity: host.Activity, Request: request, Session: host.Session, AgentContext: host.AgentContext, SuccessDuration: host.SuccessDuration, StreamClock: host.StreamClock, StreamPolicy: host.StreamPolicy, Emit: func(value modelyield.Yield) (requestretry.NoticeAction, error) {
		var yield Yield
		var original *streamhistory.Envelope
		if value.Notice != nil {
			if host.RetryAPIError != nil {
				host.RetryAPIError(value.Notice.Error)
			}
			record, err := value.Notice.Envelope()
			if err != nil {
				return 0, err
			}
			yield.Message = &Message{Record: record}
			yield.APIRetryNotice = true
			yield.APIRetryError = value.Notice.Error
		} else if value.Control != nil {
			yield.Message = &Message{Record: *value.Control}
		} else if value.Update != nil {
			original = value.Update.Envelope
			if original == nil {
				yield.Stream = value.Update
			} else {
				// Source withholds this category for its separate output-limit
				// recovery. A completed synthetic envelope is not enough to
				// claim that recovery/continuation policy has migrated.
				if string(original.Record.Get("apiError")) == `"max_output_tokens"` {
					return 0, unsupported("output-limit recovery", ErrUnsupported)
				}
				var observable *streamhistory.Envelope
				var err error
				if host.LookupBackfillJSON != nil {
					observable, err = observableEnvelopeJSON(original, host.LookupBackfillJSON)
				} else {
					observable, err = observableEnvelope(original, host.LookupBackfill)
				}
				if err != nil {
					return 0, err
				}
				yield.Message = &Message{Assistant: observable}
			}
		} else {
			return 0, errors.New("shared model adapter returned no yield")
		}
		if err := emit(yield); err != nil {
			if stopped() && err == requestretry.ErrConsumerStopped {
				return requestretry.Stop, nil
			}
			return 0, err
		}
		// Generator resume is the acceptance boundary. Keep the original
		// live envelope; the display backfill clone never becomes model history.
		if original != nil {
			assistants = append(assistants, original)
			for _, block := range original.Content {
				if block.Object.String("type") == "tool_use" {
					uses = append(uses, Invocation{block, original})
				}
			}
		}
		return requestretry.Resume, nil
	}})
	return assistants, uses, outcome, err
}
