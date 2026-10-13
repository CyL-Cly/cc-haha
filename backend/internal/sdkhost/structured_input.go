package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"strings"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
)

// PlainStructuredInput ports StructuredIO.read's actual initial split,
// for-await raw blocks, splitAndProcess delegation and async processLine.
// Session controls, environment updates, prepend/remote IO and consumer Return
// remain explicit capabilities. Original bytes are parsed here, not supplied as
// a pre-resolved Source message or an expected phase schedule.
type PlainStructuredInput struct {
	remote                 *remoteio.Service
	environmentUpdate      func(*cooperative.Scope, protocol.JSONObject) error
	allowSideQuestion      bool
	allowSDKInitialize     bool
	allowSDKMCP            bool
	allowSDKMCPControl     bool
	allowSDKGetSettings    bool
	allowSDKMCPStatus      bool
	allowSDKInference      bool
	allowSDKPermissionMode bool
	sandboxResponse        func(*cooperative.Scope, protocol.JSONObject) (bool, error)
	sandboxClose           func(*cooperative.Scope) error
	sandboxReplay          bool
	owner                  *cooperative.Owner
	raw                    *OwnerIterator[string]
	read                   *OwnerIterator[protocol.JSONObject]
	content                string
	diagnostics            *diagnostics.Process
	allowStopTask          bool
	allowAgentMessage      bool
	allowSessionInbox      bool
	allowSessionUsage      bool
	allowContextUsage      bool
	prepended              []string
}

type parsedLine struct {
	message protocol.JSONObject
	present bool
}

func NewPlainStructuredInput(owner *cooperative.Owner, raw *OwnerIterator[string]) (*PlainStructuredInput, error) {
	return NewPlainStructuredInputWithDiagnostics(owner, raw, nil)
}

// The process sink is borrowed; parsing keeps its original Owner/iterator.
func NewPlainStructuredInputWithDiagnostics(owner *cooperative.Owner, raw *OwnerIterator[string], sink *diagnostics.Process) (*PlainStructuredInput, error) {
	if owner == nil || raw == nil || raw.AsyncIteratorOwner() != owner {
		return nil, cooperative.ErrRestricted
	}
	input := &PlainStructuredInput{owner: owner, raw: raw, diagnostics: sink}
	read, err := NewOwnerIterator(owner, func(scope *cooperative.Scope, iterator *OwnerIterator[protocol.JSONObject], request IteratorRequest[protocol.JSONObject]) error {
		if request.Returning {
			return cap("StructuredIO consumer return")
		}
		return input.split(scope, iterator)
	})
	input.read = read
	return input, err
}

func (input *PlainStructuredInput) Next(scope *cooperative.Scope) (*OwnerFuture[IteratorResult[protocol.JSONObject]], error) {
	return input.read.Next(scope)
}

func (input *PlainStructuredInput) Iterator() *OwnerIterator[protocol.JSONObject] { return input.read }

func (input *PlainStructuredInput) split(scope *cooperative.Scope, read *OwnerIterator[protocol.JSONObject]) error {
	var split *OwnerIterator[protocol.JSONObject]
	var step GeneratorContinuation[protocol.JSONObject]
	step = func(scope *cooperative.Scope, iterator *OwnerIterator[protocol.JSONObject], request IteratorRequest[protocol.JSONObject]) error {
		if request.Returning {
			return cap("StructuredIO consumer return")
		}
		if len(input.prepended) > 0 {
			input.content = strings.Join(input.prepended, "") + input.content
			input.prepended = nil
		}
		newline := strings.IndexByte(input.content, '\n')
		if newline < 0 {
			return iterator.Complete(scope)
		}
		line := input.content[:newline]
		input.content = input.content[newline+1:]
		parsed, err := input.processLine(scope, line)
		if err != nil {
			return err
		}
		return AwaitGenerator(scope, iterator, parsed, func(next *cooperative.Scope, value parsedLine) error {
			if !value.present {
				return step(next, iterator, IteratorRequest[protocol.JSONObject]{})
			}

			if input.diagnostics != nil {
				data := protocol.NewJSONObject()
				if kind := value.message.Get("type"); kind != nil {
					data.Set("type", kind)
				}
				if err := input.diagnostics.Log("info", "cli_stdin_message_parsed", &data); err != nil {
					return err
				}
			}
			return iterator.Yield(next, value.message, step)
		}, nil)
	}
	var err error
	split, err = NewOwnerIterator(input.owner, step)
	if err != nil {
		return err
	}
	return read.Delegate(scope, split, IteratorRequest[protocol.JSONObject]{}, func(next *cooperative.Scope, _ IteratorResult[protocol.JSONObject], request IteratorRequest[protocol.JSONObject]) error {
		if request.Returning {
			return cap("StructuredIO consumer return")
		}
		return input.nextRaw(next, read)
	}, nil)
}

func (input *PlainStructuredInput) nextRaw(scope *cooperative.Scope, read *OwnerIterator[protocol.JSONObject]) error {
	pending, err := input.raw.Next(scope)
	if err != nil {
		return err
	}
	return AwaitGenerator(scope, read, pending, func(next *cooperative.Scope, result IteratorResult[string]) error {
		if !result.Done {
			if !utf8.ValidString(result.Value) {
				return cap("raw SDK block split inside UTF8 code point")
			}
			input.content += result.Value
			return input.split(next, read)
		}
		if input.content == "" {
			if input.sandboxClose != nil {
				if e := input.sandboxClose(next); e != nil {
					return e
				}
			}
			return read.Complete(next)
		}
		line := input.content
		input.content = ""
		parsed, err := input.processLine(next, line)
		if err != nil {
			return err
		}
		return AwaitGenerator(next, read, parsed, func(later *cooperative.Scope, value parsedLine) error {
			if !value.present {
				if input.sandboxClose != nil {
					if e := input.sandboxClose(later); e != nil {
						return e
					}
				}
				return read.Complete(later)
			}
			return read.Yield(later, value.message, func(done *cooperative.Scope, iterator *OwnerIterator[protocol.JSONObject], request IteratorRequest[protocol.JSONObject]) error {
				if request.Returning {
					return cap("StructuredIO consumer return")
				}
				if input.sandboxClose != nil {
					if e := input.sandboxClose(done); e != nil {
						return e
					}
				}
				return iterator.Complete(done)
			})
		}, nil)
	}, nil)
}

func (input *PlainStructuredInput) processLine(scope *cooperative.Scope, line string) (*OwnerFuture[parsedLine], error) {
	if !scope.BelongsTo(input.owner) {
		return nil, cooperative.ErrRestricted
	}
	future, _ := NewOwnerFuture[parsedLine](input.owner)
	if line == "" {
		_ = future.Resolve(scope, parsedLine{})
		return future, nil
	}
	var message protocol.JSONObject
	if err := message.UnmarshalJSON([]byte(line)); err != nil || !message.IsObject() {
		return nil, cap("streaming input parse error/exit policy")
	}
	if input.allowSDKInitialize {
		normalizeNativeSDKControlKeys(&message)
	}
	switch message.String("type") {
	case "control_response":
		input.completeRemoteResponseLifecycle(message)
		if input.sandboxResponse == nil {
			return nil, cap("StructuredIO message branch " + message.String("type"))
		}
		handled, e := input.sandboxResponse(scope, message)
		if e != nil {
			return nil, e
		}
		if !handled {
			return nil, cap("StructuredIO unmatched control_response consumer")
		}
		_ = future.Resolve(scope, parsedLine{message: message, present: input.sandboxReplay && object(message.Get("response")).String("subtype") != "error"})
	case "control_request":
		subtype := object(message.Get("request")).String("subtype")
		if !(input.allowSideQuestion && (subtype == "side_question" || subtype == "cancel_side_question") || input.allowSDKInitialize && subtype == "initialize" || input.allowSDKMCP && subtype == "mcp_message" || input.allowSDKMCPControl && (subtype == "mcp_set_servers" || subtype == "mcp_reconnect" || subtype == "mcp_toggle") || input.allowSDKGetSettings && subtype == "get_settings" || input.allowSDKMCPStatus && subtype == "mcp_status" || input.allowSDKInference && (subtype == "set_model" || subtype == "set_max_thinking_tokens") || input.allowSDKPermissionMode && subtype == "set_permission_mode" || input.allowContextUsage && subtype == "get_context_usage" || input.allowSessionUsage && subtype == "get_session_usage" || input.allowStopTask && subtype == "stop_task" || input.allowAgentMessage && subtype == "send_agent_message" || input.allowSessionInbox && (subtype == "enqueue_session_message" || subtype == "interrupt" || subtype == "end_session")) {
			return nil, cap("StructuredIO message branch " + message.String("type"))
		}
		_ = future.Resolve(scope, parsedLine{message: message, present: true})
	case "keep_alive":
		_ = future.Resolve(scope, parsedLine{})
	case "update_environment_variables":
		if input.environmentUpdate == nil {
			return nil, cap("StructuredIO environment update consumer")
		}
		variables := object(message.Get("variables"))
		if err := input.environmentUpdate(scope, variables); err != nil {
			return nil, err
		}
		_ = future.Resolve(scope, parsedLine{})
	case "user":
		if object(message.Get("message")).String("role") != "user" {
			return nil, cap("streaming input role error/exit policy")
		}
		_ = future.Resolve(scope, parsedLine{message: message, present: true})
	default:
		return nil, cap("StructuredIO message branch " + message.String("type"))
	}
	return future, nil
}

// InternalEventsPending is the original Plain StructuredIO getter. Remote IO
// is a different class and is not accepted as this input implementation.
func (input *PlainStructuredInput) InternalEventsPending(scope *cooperative.Scope) (int, error) {
	if input == nil || !scope.BelongsTo(input.owner) {
		return 0, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return 0, err
	}
	if input.remote != nil {
		return input.remote.InternalEventsPending(), nil
	}
	return 0, nil
}

// FlushInternalEvents returns the actual already-fulfilled PlainIO Promise.
// Its caller performs the one Source await; no internal event drain is added.
func (input *PlainStructuredInput) FlushInternalEvents(scope *cooperative.Scope) (*OwnerFuture[struct{}], error) {
	if input == nil || !scope.BelongsTo(input.owner) {
		return nil, cooperative.ErrRestricted
	}
	if input.remote != nil {
		return input.flushRemoteInternalEvents(scope)
	}
	future, err := NewOwnerFuture[struct{}](input.owner)
	if err != nil {
		return nil, err
	}
	if err = future.Resolve(scope, struct{}{}); err != nil {
		_ = future.RejectAdministrative(err)
		return nil, err
	}
	return future, nil
}
