package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/prompthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"strings"
)

type EventResult struct {
	Fields  protocol.JSONObject
	Message *queryloop.Message
	Outcome string
	Hook    protocol.JSONObject
}
type CommandEvent struct {
	Event, SessionID, ToolUseID, MatchQuery string
	Fields                                  protocol.JSONObject
	Call                                    Context
	IfMatcher                               func(json.RawMessage) (bool, error)
}

func (s *Service) NativeEventPresent(event, sid string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.simple() {
		return false, nil
	}
	rows, err := s.configuredNativeCommands(event, sid)
	if err != nil {
		return false, err
	}
	return !s.resolver.disabled && (len(rows) > 0 || s.activeCallbacks(event, sid)), nil
}

// ExecuteCommandEvent consumes original event fields on the existing native
// graph. It does not accept Hook decisions, rendered messages or model output.
func (s *Service) ExecuteCommandEvent(ctx context.Context, event CommandEvent, consume func(EventResult) error) error {
	b := s.options.Commands
	if ctx == nil || consume == nil || b == nil || event.Call.Scope == nil || !event.Call.Scope.BelongsTo(b.Owner) {
		return cooperative.ErrRestricted
	}
	if event.Event == "" || event.ToolUseID == "" || event.SessionID == "" {
		return errors.New("native Hook event requires original event/session/tool identity")
	}
	s.mu.Lock()
	rows, e := s.configuredNativeCommands(event.Event, event.SessionID)
	if e == nil && ctx.Err() == nil && s.activeCallbacks(event.Event, event.SessionID) {
		e = unsupported("native event callback/function execution")
	}
	// The original executor resolves matching hooks, then returns without
	// spawning or producing messages when the incoming signal is already aborted.
	skip := s.resolver.disabled || s.simple() || event.Call.Interactive && !event.Call.TrustAccepted || ctx.Err() != nil
	s.mu.Unlock()
	if e != nil {
		return e
	}
	if skip || len(rows) == 0 {
		return nil
	}
	call := event.Call
	call.HookEvent = event.Event
	call.HookName = event.Event
	if event.MatchQuery != "" {
		call.HookName += ":" + event.MatchQuery
	}
	call.InputFields = event.Fields.Clone()
	input, e := b.Input(ctx, call)
	if e != nil {
		return e
	}
	selected := []protocol.JSONObject{}
	indices := map[string]int{}
	for _, row := range rows {
		if event.MatchQuery != "" && !nativeCommandMatch(event.MatchQuery, row.matcher) {
			continue
		}
		hook := row.hook
		if hook.String("type") == "callback" {
			selected = append(selected, hook.Clone())
			continue
		}
		key, e := nativeEventHookKey(hook)
		if e != nil {
			return e
		}
		if i, ok := indices[key]; ok {
			selected[i] = hook.Clone()
		} else {
			indices[key] = len(selected)
			selected = append(selected, hook.Clone())
		}
	}
	// Original matching first deduplicates each kind, preserving the first key
	// position and the last declaration, then evaluates its condition.
	ordered := make([]protocol.JSONObject, 0, len(selected))
	for _, kind := range []string{"command", "prompt", "agent", "http", "callback"} {
		for _, hook := range selected {
			if hook.String("type") != kind {
				continue
			}
			if condition := hook.Get("if"); hookTruthy(condition) {
				switch event.Event {
				case "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest":
					if event.IfMatcher == nil {
						return unsupported("actual tool hook if matcher")
					}
					match, e := event.IfMatcher(condition)
					if e != nil {
						return e
					}
					if !match {
						continue
					}
				default:
					continue
				}
			}
			if kind == "http" && (event.Event == "SessionStart" || event.Event == "Setup") {
				continue
			}
			if kind != "command" && (kind != "prompt" || !s.HasNativePromptBindings(b.Owner)) && (kind != "http" || !s.HasNativeHTTPBindings(b.Owner)) && (kind != "agent" || !s.HasNativeAgentBindings(b.Owner)) && (kind != "callback" || !s.HasNativeCallbackBindings(b.Owner)) {
				return unsupported(event.Event + " hook kind " + hook.String("type"))
			}
			if kind == "command" && (string(hook.Get("async")) == "true" || string(hook.Get("asyncRewake")) == "true") && b.Async == nil {
				return unsupported("actual event asynchronous hook lifecycle")
			}
			ordered = append(ordered, hook)
		}
	}
	selected = ordered
	for _, hook := range selected {
		display := nativeEventHookDisplay(hook)
		data := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("hook_progress")}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(event.Event)}, protocol.JSONField{Name: "hookName", Value: commandJSON(call.HookName)}, protocol.JSONField{Name: "command", Value: commandJSON(display)})
		if hook.String("type") == "prompt" {
			data.Set("promptText", hook.Get("prompt"))
		}
		if hook.Get("statusMessage") != nil {
			data.Set("statusMessage", hook.Get("statusMessage"))
		}
		message := queryloop.Message{Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("progress")}, protocol.JSONField{Name: "data", Value: commandJSON(data)}, protocol.JSONField{Name: "parentToolUseID", Value: commandJSON(event.ToolUseID)}, protocol.JSONField{Name: "toolUseID", Value: commandJSON(event.ToolUseID)}, protocol.JSONField{Name: "timestamp", Value: commandJSON(b.Timestamp())}, protocol.JSONField{Name: "uuid", Value: commandJSON(s.options.UUID())})}
		if e = consume(EventResult{Message: &message}); e != nil {
			return e
		}
	}
	type completed struct {
		hook     protocol.JSONObject
		id       string
		output   CommandOutput
		failure  error
		prompt   *prompthook.Result
		http     *nativeHTTPCompletion
		callback *EventResult
		duration float64
	}
	ready := []completed{}
	remaining := len(selected)
	var wake *ownerasync.OwnerFuture[struct{}]
	for hookIndex, hook := range selected {
		job := func(scope *cooperative.Scope) error {
			current := call
			index := hookIndex
			current.HookIndex = &index
			current.Scope = scope
			current.HookID = s.options.UUID()
			if b.Event != nil && hook.String("type") != "prompt" && hook.String("type") != "agent" && hook.String("type") != "callback" {
				_ = b.Event(current, "started", current.HookID, event.Event, CommandOutput{}, "")
			}
			completion := completed{hook: hook, id: current.HookID}
			if hook.String("type") == "callback" {
				value, failure := s.runNativeSDKEvent(ctx, current, hook, input, event.ToolUseID)
				completion.callback, completion.failure = &value, failure
			} else if hook.String("type") == "prompt" {
				started := b.NowMillis()
				if current.ToolUseContext == nil {
					// This is the original executeHooks catch path, not a model
					// capability refusal or fabricated auxiliary response.
					completion.failure = errors.New("ToolUseContext is required for prompt hooks. This is a bug.")
					completion.output.DurationMillis = b.NowMillis() - started
				} else {
					value, failure := s.runNativeEventPrompt(ctx, current, hook, input, event.ToolUseID)
					completion.prompt, completion.failure, completion.duration = &value, failure, b.NowMillis()-started
				}
			} else if hook.String("type") == "agent" {
				// These wrappers omit messages in the original executeHooks
				// call. Preserve its two presence checks before any model request.
				completion.failure = errors.New("Messages are required for agent hooks. This is a bug.")
				if current.ToolUseContext == nil {
					completion.failure = errors.New("ToolUseContext is required for agent hooks. This is a bug.")
				}
			} else if hook.String("type") == "http" {
				started := b.NowMillis()
				value, failure := s.options.HTTP.Run(ctx, current, hook, input)
				completionValue, failure := s.nativeHTTPEventCompletion(current, hook, event.ToolUseID, value, failure, b.NowMillis()-started)
				completion.http, completion.failure = &completionValue, failure
			} else {
				completion.output, completion.failure = b.Run(ctx, current, hook, input)
			}
			ready = append(ready, completion)
			remaining--
			if wake != nil {
				f := wake
				wake = nil
				return f.Resolve(scope, struct{}{})
			}
			return nil
		}
		_, handled, submitErr := call.Scope.SubmitNativePermissionRequestHook(b.Owner, event.Event, job)
		if !handled {
			_, submitErr = b.Owner.SubmitIndependent(context.Background(), cooperative.Read, job)
		}
		e = submitErr
		if e != nil {
			return e
		}
	}
	permissionBehavior := ""
	for len(ready) > 0 || remaining > 0 {
		if len(ready) == 0 {
			wake, e = ownerasync.NewOwnerFuture[struct{}](b.Owner)
			if e != nil {
				return e
			}
			if _, e = wake.Await(call.Scope); e != nil {
				return e
			}
			continue
		}
		r := ready[0]
		ready = ready[1:]
		if r.callback != nil {
			if r.failure != nil {
				return r.failure
			}
			value := *r.callback
			mergeNativeEventPermission(&value.Fields, &permissionBehavior)
			if err := consume(value); err != nil {
				return err
			}
			continue
		}
		if r.http != nil {
			if r.failure != nil {
				return r.failure
			}
			if b.Event != nil {
				_ = b.Event(call, r.http.ResponseOutcome, r.id, event.Event, r.http.Response, "")
			}
			value := r.http.Event
			mergeNativeEventPermission(&value.Fields, &permissionBehavior)
			if e := consume(value); e != nil {
				return e
			}
			continue
		}
		if r.prompt != nil {
			if r.failure != nil {
				return r.failure
			}
			if e := consume(s.nativePromptEventCompletion(call, r.hook, *r.prompt, r.duration)); e != nil {
				return e
			}
			continue
		}
		display := nativeEventHookDisplay(r.hook)
		out := r.output
		if out.Backgrounded {
			if e = consume(EventResult{Outcome: "success", Hook: r.hook}); e != nil {
				return e
			}
			continue
		}
		outcome := "success"
		responseOutcome := "success"
		fields := protocol.NewJSONObject()
		attachment := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("hook_success")}, protocol.JSONField{Name: "hookName", Value: commandJSON(call.HookName)}, protocol.JSONField{Name: "toolUseID", Value: commandJSON(event.ToolUseID)}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(event.Event)})
		parsed, e := CommandStopDecision(display, event.Event, out)
		if e == nil && parsed.Async {
			if e = consume(EventResult{Outcome: "success", Hook: r.hook}); e != nil {
				return e
			}
			continue
		}
		failure := r.failure
		if failure == nil {
			failure = e
		}
		if failure != nil {
			var capability *CapabilityError
			if errors.As(failure, &capability) {
				return failure
			}
			out.Code = 1
			out.Stdout = ""
			out.Stderr = "Failed to run: " + failure.Error()
			out.Output = out.Stderr
		}
		if out.Aborted {
			outcome = "cancelled"
			responseOutcome = "cancelled"
			attachment.Set("type", commandJSON("hook_cancelled"))
		} else if failure != nil || parsed.ValidationError != "" {
			outcome = "non_blocking_error"
			responseOutcome = "error"
			if parsed.ValidationError != "" {
				out.Code = 1
				out.Stderr = "JSON validation failed: " + parsed.ValidationError
			}
			attachment.Set("type", commandJSON("hook_non_blocking_error"))
			attachment.Set("stderr", commandJSON(out.Stderr))
			attachment.Set("stdout", commandJSON(out.Stdout))
			attachment.Set("exitCode", commandJSON(out.Code))
		} else if parsed.JSON {
			if parsed.Interpreted == nil {
				return errors.New("validated Hook lost native result")
			}
			fields = parsed.Interpreted.Fields.Clone()
			attachment = parsed.Interpreted.Attachment.Clone()
			attachment.Set("hookName", commandJSON(call.HookName))
			attachment.Set("toolUseID", commandJSON(event.ToolUseID))
			if out.Code != 0 {
				// The original command generator treats interpreted JSON as a
				// successful business outcome, while its SDK response reports
				// the physical nonzero status separately.
				responseOutcome = "error"
			}
		} else if out.Code == 2 {
			outcome = "blocking"
			responseOutcome = "error"
			fields.Set("blockingError", commandJSON(protocol.NewJSONObject(protocol.JSONField{Name: "blockingError", Value: parsed.blockingJSON}, protocol.JSONField{Name: "command", Value: r.hook.Get("command")})))
			attachment = protocol.NewJSONObject()
		} else if out.Code != 0 {
			outcome = "non_blocking_error"
			responseOutcome = "error"
			attachment.Set("type", commandJSON("hook_non_blocking_error"))
			text := strings.TrimFunc(out.Stderr, javaScriptWhitespace)
			if text == "" {
				text = "No stderr output"
			}
			attachment.Set("stderr", commandJSON("Failed with non-blocking status code: "+text))
			attachment.Set("stdout", commandJSON(out.Stdout))
			attachment.Set("exitCode", commandJSON(out.Code))
		} else {
			attachment.Set("content", commandJSON(strings.TrimFunc(out.Stdout, javaScriptWhitespace)))
			attachment.Set("stdout", commandJSON(out.Stdout))
			attachment.Set("stderr", commandJSON(out.Stderr))
			attachment.Set("exitCode", commandJSON(out.Code))
		}
		if b.Event != nil {
			_ = b.Event(call, responseOutcome, r.id, event.Event, out, "")
		}
		var message *queryloop.Message
		if len(attachment.OwnProperties()) > 0 {
			if !parsed.JSON {
				attachment.Set("command", commandJSON(display))
				attachment.Set("durationMs", commandJSON(out.DurationMillis))
			}
			record := queryloop.Message{Record: b.Factory.CreateAttachment(commandJSON(attachment))}
			message = &record
		}
		mergeNativeEventPermission(&fields, &permissionBehavior)
		if e = consume(EventResult{Fields: fields, Message: message, Outcome: outcome, Hook: r.hook}); e != nil {
			return e
		}
	}
	return nil
}

func mergeNativeEventPermission(fields *protocol.JSONObject, permissionBehavior *string) {
	ownBehavior := fields.String("permissionBehavior")
	switch ownBehavior {
	case "deny":
		*permissionBehavior = "deny"
	case "ask":
		if *permissionBehavior != "deny" {
			*permissionBehavior = "ask"
		}
	case "allow":
		if *permissionBehavior == "" {
			*permissionBehavior = "allow"
		}
	}
	if ownBehavior == "" && fields.Get("updatedInput") != nil {
		fields.Set("passthroughUpdatedInput", fields.Get("updatedInput"))
	}
	if *permissionBehavior != "" {
		fields.Set("permissionBehavior", commandJSON(*permissionBehavior))
		fields.Set("hookSource", commandJSON("settings"))
		if ownBehavior != "allow" && ownBehavior != "ask" {
			fields.Delete("updatedInput")
		}
	}
}
