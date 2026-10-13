package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/prompthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func commandJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func commandStringConcat(prefix string, raw json.RawMessage) json.RawMessage {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return commandJSON(prefix)
	}
	return protocol.QuoteJavaScriptUTF16(append(utf16.Encode([]rune(prefix)), units...))
}
func (s *Service) emitCommandAttachment(emit queryloop.Emit, kind, event, id string, fields ...protocol.JSONField) error {
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON(kind)}, protocol.JSONField{Name: "hookName", Value: commandJSON(event)}, protocol.JSONField{Name: "toolUseID", Value: commandJSON(id)}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(event)})
	for _, f := range fields {
		payload.Set(f.Name, f.Value)
	}
	message := queryloop.Message{Record: s.options.Commands.Factory.CreateAttachment(commandJSON(payload))}
	return emit(queryloop.Yield{Message: &message})
}

func (s *Service) executeNativeCommandStop(ctx context.Context, call Context, event, id string, matchers []commandMatcher, emit queryloop.Emit) (queryloop.StopDecision, error) {
	b := s.options.Commands
	decision := queryloop.StopDecision{BlockingMessages: []queryloop.Message{}, AllowBlockingContinuation: true}
	if b == nil || call.Scope == nil || !call.Scope.BelongsTo(b.Owner) {
		return decision, cooperative.ErrRestricted
	}
	input, e := b.Input(ctx, call)
	if e != nil {
		return decision, e
	}
	selected, e := selectNativeStopCommands(event, input, matchers, s.HasNativePromptBindings(b.Owner), s.HasNativeHTTPBindings(b.Owner), s.HasNativeAgentBindings(b.Owner), b.Async != nil, s.HasNativeCallbackBindings(b.Owner))
	if e != nil {
		return decision, e
	}
	if len(selected) == 0 {
		return decision, nil
	}
	for _, hook := range selected {
		command := nativeEventHookDisplay(hook)
		data := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("hook_progress")}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(event)}, protocol.JSONField{Name: "hookName", Value: commandJSON(event)}, protocol.JSONField{Name: "command", Value: commandJSON(command)})
		if hook.String("type") == "prompt" {
			data.Set("promptText", hook.Get("prompt"))
		}
		if raw := hook.Get("statusMessage"); raw != nil && string(raw) != "null" {
			data.Set("statusMessage", raw)
		}
		timestamp := b.Timestamp()
		uuid := s.options.UUID()
		message := queryloop.Message{Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("progress")}, protocol.JSONField{Name: "data", Value: commandJSON(data)}, protocol.JSONField{Name: "parentToolUseID", Value: commandJSON(id)}, protocol.JSONField{Name: "toolUseID", Value: commandJSON(id)}, protocol.JSONField{Name: "timestamp", Value: commandJSON(timestamp)}, protocol.JSONField{Name: "uuid", Value: commandJSON(uuid)})}
		if e := emit(queryloop.Yield{Message: &message}); e != nil {
			return decision, e
		}
	}
	type result struct {
		hook     protocol.JSONObject
		hookID   string
		output   CommandOutput
		failure  error
		prompt   *prompthook.Result
		http     *nativeHTTPCompletion
		callback *EventResult
	}
	ready := []result{}
	remaining := len(selected)
	var wake *ownerasync.OwnerFuture[struct{}]
	for hookIndex, hook := range selected {
		job := func(scope *cooperative.Scope) error {
			current := call
			index := hookIndex
			current.HookIndex = &index
			current.Scope = scope
			hookID := s.options.UUID()
			current.HookID, current.HookEvent = hookID, event
			current.HookName = event
			if b.Event != nil && hook.String("type") != "prompt" && hook.String("type") != "agent" && hook.String("type") != "callback" {
				_ = b.Event(current, "started", hookID, event, CommandOutput{}, hook.String("statusMessage"))
			}
			var output CommandOutput
			var failure error
			var promptResult *prompthook.Result
			var httpResult *nativeHTTPCompletion
			var callbackResult *EventResult
			if hook.String("type") == "callback" {
				value, err := s.runNativeSDKEvent(ctx, current, hook, input, id)
				callbackResult, failure = &value, err
			} else if hook.String("type") == "prompt" || hook.String("type") == "agent" {
				current.HookName, current.HookEvent = event, event
				started := b.NowMillis()
				// Source's batch timer is scheduled before execPromptHook's own
				// timer. Its cancellation is the inner hook's parent abort.
				outerTimeout := 10 * time.Minute
				var seconds float64
				_ = json.Unmarshal(hook.Get("timeout"), &seconds)
				if seconds != 0 {
					outerTimeout = time.Duration(seconds * float64(time.Second))
				}
				outer, cleanup := context.WithTimeout(ctx, outerTimeout)
				var value prompthook.Result
				var err error
				if hook.String("type") == "agent" {
					value, err = s.executeNativeAgentHook(outer, current, hook, input, id)
				} else {
					value, err = s.executeNativePromptHook(outer, current, hook, input, id)
				}
				cleanup()
				output.DurationMillis = b.NowMillis() - started
				failure = err
				promptResult = &value
			} else if hook.String("type") == "http" {
				started := b.NowMillis()
				value, err := s.options.HTTP.Run(ctx, current, hook, input)
				completion, err := s.nativeHTTPEventCompletion(current, hook, id, value, err, b.NowMillis()-started)
				httpResult, failure = &completion, err
			} else {
				output, failure = b.Run(ctx, current, hook, input)
			}
			ready = append(ready, result{hook: hook, hookID: hookID, output: output, failure: failure, prompt: promptResult, http: httpResult, callback: callbackResult})
			remaining--
			if wake != nil {
				f := wake
				wake = nil
				return f.Resolve(scope, struct{}{})
			}
			return nil
		}
		_, handled, submitErr := call.Scope.SubmitNativeSideQuestionStopHook(context.Background(), b.Owner, event, job)
		if !handled {
			_, submitErr = b.Owner.SubmitIndependent(context.Background(), cooperative.Read, job)
		}
		e = submitErr
		if e != nil {
			return decision, e
		}
	}
	errorsOut := []json.RawMessage{}
	infos := []protocol.JSONObject{}
	pendingReason := commandJSON("")
	hasPending := false
	stopReason := commandJSON("")
	hasOutput := false
	goalCompleted := false
	goalBlockingCount := 0
	goalContinuation := ""
	var goalContinuationRaw json.RawMessage
	feedback := func(text json.RawMessage) error {
		message := queryloop.Message{Record: messages.OrderedUser(b.Factory.CreateUser(storage.Object{"content": commandStringConcat("Stop hook feedback:\n", text), "isMeta": json.RawMessage("true")}))}
		decision.BlockingMessages = append(decision.BlockingMessages, message)
		errorsOut = append(errorsOut, text)
		hasOutput = true
		return emit(queryloop.Yield{Message: &message})
	}
	stopped := func(reason json.RawMessage) error {
		decision.PreventContinuation = true
		stopReason = reason
		return s.emitCommandAttachment(emit, "hook_stopped_continuation", "Stop", id, protocol.JSONField{Name: "message", Value: reason})
	}
	for len(ready) > 0 || remaining > 0 {
		if len(ready) == 0 {
			wake, e = ownerasync.NewOwnerFuture[struct{}](b.Owner)
			if e != nil {
				return decision, e
			}
			if _, e = wake.Await(call.Scope); e != nil {
				return decision, e
			}
			continue
		}
		current := ready[0]
		ready = ready[1:]
		hook, out := current.hook, current.output
		if out.Backgrounded {
			continue
		}
		if current.http != nil || current.callback != nil {
			if current.failure != nil {
				return decision, current.failure
			}
			value := EventResult{}
			if current.http != nil {
				value = current.http.Event
				if b.Event != nil {
					_ = b.Event(call, current.http.ResponseOutcome, current.hookID, event, current.http.Response, "")
				}
			} else {
				value = *current.callback
			}
			fields := value.Fields
			blocked, _ := fields.Object("blockingError")
			blocking := blocked.Get("blockingError")
			prevent := hookTruthy(fields.Get("preventContinuation"))
			if prevent && hookTruthy(blocking) {
				if e = feedback(blocking); e != nil {
					return decision, e
				}
				reason := fields.Get("stopReason")
				if !hookTruthy(reason) {
					reason = commandJSON("Stop hook prevented continuation")
				}
				if e = stopped(reason); e != nil {
					return decision, e
				}
			}
			if hookTruthy(blocking) {
				if e = feedback(blocking); e != nil {
					return decision, e
				}
			}
			if prevent && !hookTruthy(blocking) {
				hasPending = true
				pendingReason = fields.Get("stopReason")
				if !hookTruthy(pendingReason) {
					pendingReason = commandJSON("Stop hook prevented continuation")
				}
			}
			info := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: commandJSON(nativeEventHookDisplay(hook))})
			if value.Message != nil {
				attachment, _ := value.Message.Record.Object("attachment")
				if duration := attachment.Get("durationMs"); duration != nil {
					info.Set("durationMs", duration)
				}
				if attachment.String("type") == "hook_non_blocking_error" {
					text := attachment.Get("stderr")
					if !hookTruthy(text) {
						code := 0
						if current.http != nil {
							code = current.http.Response.Code
						}
						text = commandJSON(fmt.Sprintf("Exit code %d", code))
					}
					errorsOut = append(errorsOut, text)
					hasOutput = true
				}
				if attachment.String("type") == "hook_success" {
					hasOutput = hasOutput || strings.TrimFunc(attachment.String("stdout"), javaScriptWhitespace) != "" || strings.TrimFunc(attachment.String("stderr"), javaScriptWhitespace) != ""
				}
				if e = emit(queryloop.Yield{Message: value.Message}); e != nil {
					return decision, e
				}
			}
			if hookTruthy(fields.Get("systemMessage")) {
				if e = s.emitCommandAttachment(emit, "hook_system_message", event, id, protocol.JSONField{Name: "content", Value: fields.Get("systemMessage")}); e != nil {
					return decision, e
				}
			}
			infos = append(infos, info)
			if value.Outcome == "cancelled" && ctx.Err() != nil {
				message := queryloop.Message{Record: messages.OrderedUser(b.Factory.CreateUserInterruption(false))}
				if e = emit(queryloop.Yield{Message: &message}); e != nil {
					return decision, e
				}
				decision.PreventContinuation = true
				decision.BlockingMessages = []queryloop.Message{}
				return decision, nil
			}
			continue
		}
		if current.prompt != nil {
			if current.failure != nil {
				return decision, current.failure
			}
			value := current.prompt
			blockingJSON := value.BlockingErrorRaw
			if len(blockingJSON) == 0 {
				blockingJSON = commandJSON(value.BlockingError)
			}
			command := hook.String("statusMessage")
			if command == "" {
				command = hook.String("prompt")
			}
			info := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: commandJSON(command)})
			if hook.String("type") == "prompt" {
				info.Set("promptText", hook.Get("prompt"))
			}
			if value.Outcome == "success" {
				sid := s.mainSessionID()
				if call.AgentID != "" {
					sid = call.AgentID
				}
				if hook.String("type") == "prompt" {
					if e = s.promptHookSucceeded(call.Scope, sid, event, hook); e != nil {
						return decision, e
					}
				}
				goalCompleted = goalCompleted || goalstate.IsGoalPrompt(command)
			}
			if value.BlockingError != "" {
				if e = feedback(blockingJSON); e != nil {
					return decision, e
				}
				if goalstate.IsGoalPrompt(value.Command) {
					goalBlockingCount++
				}
				if goalstate.IsGoalPrompt(value.Command) && goalContinuation == "" {
					goalContinuation = value.BlockingError
					goalContinuationRaw = blockingJSON
				}
			}
			if value.PreventContinuation && !goalstate.IsGoalPrompt(value.Command) {
				reason := value.StopReason
				if len(reason) == 0 {
					reason = commandJSON("Stop hook prevented continuation")
				}
				if e = stopped(reason); e != nil {
					return decision, e
				}
			}
			// executeHooks yields preventContinuation (with the blocking error)
			// before its separate blockingError yield. Both are consumed by Source.
			if value.PreventContinuation && value.BlockingError != "" {
				if e = feedback(blockingJSON); e != nil {
					return decision, e
				}

				if goalstate.IsGoalPrompt(value.Command) {
					goalBlockingCount++
				}
			}
			if value.Payload != nil {
				value.Payload.Set("command", commandJSON(command))
				value.Payload.Set("durationMs", commandJSON(out.DurationMillis))
				info.Set("durationMs", commandJSON(out.DurationMillis))
				if value.Payload.String("type") == "hook_non_blocking_error" {
					text := value.Payload.Get("stderr")
					if len(text) == 0 || string(text) == `""` || string(text) == "null" {
						text = commandJSON("Exit code 1")
					}
					errorsOut = append(errorsOut, text)
					hasOutput = true
				}
				message := queryloop.Message{Record: b.Factory.CreateAttachment(commandJSON(value.Payload))}
				if e = emit(queryloop.Yield{Message: &message}); e != nil {
					return decision, e
				}
			}
			if ctx.Err() != nil {
				message := queryloop.Message{Record: messages.OrderedUser(b.Factory.CreateUserInterruption(false))}
				if e = emit(queryloop.Yield{Message: &message}); e != nil {
					return decision, e
				}
				decision.PreventContinuation = true
				decision.BlockingMessages = []queryloop.Message{}
				return decision, nil
			}
			infos = append(infos, info)
			continue
		}
		command := hook.String("statusMessage")
		if command == "" {
			command = hook.String("command")
		}
		info := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: commandJSON(command)})
		if current.failure != nil {
			var cap *CapabilityError
			if errors.As(current.failure, &cap) {
				return decision, current.failure
			}
			out.Code = 1
			out.Stderr = "Failed to run: " + current.failure.Error()
		}
		if out.Aborted {
			if b.Event != nil {
				_ = b.Event(call, "cancelled", current.hookID, event, out, command)
			}
			info.Set("durationMs", commandJSON(out.DurationMillis))
			infos = append(infos, info)
			if e = s.emitCommandAttachment(emit, "hook_cancelled", event, id, protocol.JSONField{Name: "command", Value: commandJSON(command)}, protocol.JSONField{Name: "durationMs", Value: commandJSON(out.DurationMillis)}); e != nil {
				return decision, e
			}
			if ctx.Err() != nil {
				message := queryloop.Message{Record: messages.OrderedUser(b.Factory.CreateUserInterruption(false))}
				if e = emit(queryloop.Yield{Message: &message}); e != nil {
					return decision, e
				}
				decision.PreventContinuation = true
				decision.BlockingMessages = []queryloop.Message{}
				return decision, nil
			}
			continue
		}
		parsed, failure := CommandStopDecision(command, event, out)
		if failure == nil && parsed.Async {
			continue
		}
		if failure != nil {
			var cap *CapabilityError
			if errors.As(failure, &cap) {
				return decision, failure
			}
			out.Code = 1
			out.Stderr = "Failed to run: " + failure.Error()
			parsed = CommandDecision{}
		}
		if parsed.ValidationError != "" {
			out.Code = 1
			out.Stderr = "JSON validation failed: " + parsed.ValidationError
		}
		if b.Event != nil {
			outcome := "success"
			if out.Code != 0 {
				outcome = "error"
			}
			_ = b.Event(call, outcome, current.hookID, event, out, command)
		}
		if parsed.PreventContinuation && parsed.BlockingError != "" {
			if e = feedback(parsed.blockingJSON); e != nil {
				return decision, e
			}
			reason := parsed.stopReasonJSON
			if parsed.StopReason == "" {
				reason = commandJSON("Stop hook prevented continuation")
			}
			if e = stopped(reason); e != nil {
				return decision, e
			}
		}
		if parsed.BlockingError != "" {
			if e = feedback(parsed.blockingJSON); e != nil {
				return decision, e
			}
		}
		if parsed.PreventContinuation && parsed.BlockingError == "" {
			hasPending = true
			pendingReason = parsed.stopReasonJSON
			if parsed.StopReason == "" {
				pendingReason = commandJSON("Stop hook prevented continuation")
			}
		}
		if parsed.JSON {
			if parsed.BlockingError != "" {
				blocked := protocol.NewJSONObject(protocol.JSONField{Name: "blockingError", Value: parsed.blockingJSON}, protocol.JSONField{Name: "command", Value: commandJSON(command)})
				if e = s.emitCommandAttachment(emit, "hook_blocking_error", event, id, protocol.JSONField{Name: "blockingError", Value: commandJSON(blocked)}); e != nil {
					return decision, e
				}
			} else {
				info.Set("durationMs", commandJSON(out.DurationMillis))
				if e = s.emitCommandAttachment(emit, "hook_success", event, id, protocol.JSONField{Name: "content", Value: commandJSON("")}, protocol.JSONField{Name: "stdout", Value: commandJSON(out.Stdout)}, protocol.JSONField{Name: "stderr", Value: commandJSON(out.Stderr)}, protocol.JSONField{Name: "exitCode", Value: commandJSON(out.Code)}, protocol.JSONField{Name: "command", Value: commandJSON(command)}, protocol.JSONField{Name: "durationMs", Value: commandJSON(out.DurationMillis)}); e != nil {
					return decision, e
				}
			}
		}
		if parsed.SystemMessage != "" {
			if e = s.emitCommandAttachment(emit, "hook_system_message", event, id, protocol.JSONField{Name: "content", Value: parsed.systemJSON}); e != nil {
				return decision, e
			}
		}
		if !parsed.JSON && parsed.BlockingError == "" {
			kind := "hook_success"
			content := strings.TrimFunc(out.Stdout, javaScriptWhitespace)
			if out.Code != 0 {
				kind = "hook_non_blocking_error"
				if current.failure == nil && failure == nil && parsed.ValidationError == "" {
					text := strings.TrimFunc(out.Stderr, javaScriptWhitespace)
					if text == "" {
						text = "No stderr output"
					}
					out.Stderr = "Failed with non-blocking status code: " + text
				}
				errorsOut = append(errorsOut, commandJSON(out.Stderr))
				hasOutput = true
			} else {
				hasOutput = hasOutput || content != "" || strings.TrimFunc(out.Stderr, javaScriptWhitespace) != ""
			}
			info.Set("durationMs", commandJSON(out.DurationMillis))
			fields := []protocol.JSONField{}
			if kind == "hook_success" {
				fields = append(fields, protocol.JSONField{Name: "content", Value: commandJSON(content)}, protocol.JSONField{Name: "stdout", Value: commandJSON(out.Stdout)}, protocol.JSONField{Name: "stderr", Value: commandJSON(out.Stderr)})
			} else {
				fields = append(fields, protocol.JSONField{Name: "stderr", Value: commandJSON(out.Stderr)}, protocol.JSONField{Name: "stdout", Value: commandJSON(out.Stdout)})
			}
			fields = append(fields, protocol.JSONField{Name: "exitCode", Value: commandJSON(out.Code)}, protocol.JSONField{Name: "command", Value: commandJSON(command)}, protocol.JSONField{Name: "durationMs", Value: commandJSON(out.DurationMillis)})
			if e = s.emitCommandAttachment(emit, kind, event, id, fields...); e != nil {
				return decision, e
			}
		}
		infos = append(infos, info)
	}
	if hasPending {
		if e = stopped(pendingReason); e != nil {
			return decision, e
		}
	}
	// Infos follow progress order; completion duration is matched back to the
	// first unassigned command as handleStopHooks does for parallel callbacks.
	ordered := []protocol.JSONObject{}
	used := make([]bool, len(infos))
	for _, hook := range selected {
		name := nativeEventHookDisplay(hook)
		for i, info := range infos {
			if !used[i] && info.String("command") == name {
				ordered = append(ordered, info)
				used[i] = true
				break
			}
		}
	}
	timestamp := b.Timestamp()
	uuid := s.options.UUID()
	summary := queryloop.Message{Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("system")}, protocol.JSONField{Name: "subtype", Value: commandJSON("stop_hook_summary")}, protocol.JSONField{Name: "hookCount", Value: commandJSON(len(selected))}, protocol.JSONField{Name: "hookInfos", Value: commandJSON(ordered)}, protocol.JSONField{Name: "hookErrors", Value: commandJSON(errorsOut)}, protocol.JSONField{Name: "preventedContinuation", Value: commandJSON(decision.PreventContinuation)}, protocol.JSONField{Name: "stopReason", Value: commandJSON(stopReason)}, protocol.JSONField{Name: "hasOutput", Value: commandJSON(hasOutput)}, protocol.JSONField{Name: "level", Value: commandJSON("suggestion")}, protocol.JSONField{Name: "timestamp", Value: commandJSON(timestamp)}, protocol.JSONField{Name: "uuid", Value: commandJSON(uuid)}, protocol.JSONField{Name: "toolUseID", Value: commandJSON(id)})}
	if e = emit(queryloop.Yield{Message: &summary}); e != nil {
		return decision, e
	}
	if goalCompleted {
		message := queryloop.Message{Record: b.Factory.CreateLocalCommandInput("<local-command-stdout>Goal marked complete.</local-command-stdout>")}
		if e = emit(queryloop.Yield{Message: &message}); e != nil {
			return decision, e
		}
	}
	if goalContinuation != "" {
		output := goalstate.ContinuationOutputRawJSON(goalContinuationRaw)
		if !decision.PreventContinuation && call.AgentID == "" && len(decision.BlockingMessages) > 0 && goalBlockingCount == len(decision.BlockingMessages) && s.options.Prompts != nil && s.options.Prompts.TeamWorkInProgress != nil {
			pending, err := s.options.Prompts.TeamWorkInProgress(ctx, call)
			if err != nil {
				return decision, err
			}
			if pending {
				output = goalstate.WaitingForTeamOutputRawJSON(goalContinuationRaw)
				decision.BlockingMessages = []queryloop.Message{}
			}
		}
		text := commandStringConcat("<local-command-stdout>", output)
		units, _ := protocol.DecodeJavaScriptJSONString(text)
		text = protocol.QuoteJavaScriptUTF16(append(units, utf16.Encode([]rune("</local-command-stdout>"))...))
		record := b.Factory.CreateLocalCommandInput("")
		record.Set("content", text)
		message := queryloop.Message{Record: record}
		if e = emit(queryloop.Yield{Message: &message}); e != nil {
			return decision, e
		}
	}
	if decision.PreventContinuation {
		decision.BlockingMessages = []queryloop.Message{}
	}
	return decision, nil
}
