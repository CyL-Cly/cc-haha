package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type OutsideResult struct {
	Command       json.RawMessage `json:"command"`
	Succeeded     bool            `json:"succeeded"`
	Output        json.RawMessage `json:"output"`
	Blocked       bool            `json:"blocked"`
	WatchPaths    json.RawMessage `json:"watchPaths,omitempty"`
	SystemMessage json.RawMessage `json:"systemMessage,omitempty"`
}
type OutsideInput struct {
	Event, SessionID, MatchQuery string
	Fields                       protocol.JSONObject
	Call                         Context
	Timeout                      time.Duration
}

// ExecuteOutside runs each actual peer/process concurrently and awaits the
// original Promise.all input order. It never uses the REPL result projector.
func (s *Service) ExecuteOutside(ctx context.Context, in OutsideInput) ([]OutsideResult, error) {
	b := s.options.Commands
	if b == nil || in.Call.Scope == nil || !in.Call.Scope.BelongsTo(b.Owner) {
		return nil, cooperative.ErrRestricted
	}
	s.mu.Lock()
	if s.simple() {
		s.mu.Unlock()
		return []OutsideResult{}, nil
	}
	rows, err := s.configuredNativeCommands(in.Event, in.SessionID)
	skip := s.resolver.disabled || in.Call.Interactive && !in.Call.TrustAccepted || ctx.Err() != nil
	callbacks := s.activeCallbacks(in.Event, in.SessionID)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if skip {
		return []OutsideResult{}, nil
	}
	if callbacks {
		return nil, unsupported("outside-REPL callback/function producer")
	}
	sdkMatchers := s.sdkCallbackMatchers(in.Event)
	selected := []protocol.JSONObject{}
	keys := map[string]int{}
	for _, row := range rows {
		if in.MatchQuery != "" && !nativeCommandMatch(in.MatchQuery, row.matcher) {
			continue
		}
		key, e := nativeEventHookKey(row.hook)
		if e != nil {
			return nil, e
		}
		if i, ok := keys[key]; ok {
			selected[i] = row.hook.Clone()
		} else {
			keys[key] = len(selected)
			selected = append(selected, row.hook.Clone())
		}
	}
	ordered := []protocol.JSONObject{}
	for _, kind := range []string{"command", "prompt", "agent", "http"} {
		for _, hook := range selected {
			if hook.String("type") == kind && !hookTruthy(hook.Get("if")) {
				ordered = append(ordered, hook)
			}
		}
	}
	for _, matcher := range sdkMatchers {
		var hooks []protocol.JSONObject
		if e := json.Unmarshal(matcher.Get("hooks"), &hooks); e != nil {
			return nil, e
		}
		ordered = append(ordered, hooks...)
	}
	if len(ordered) == 0 {
		return []OutsideResult{}, nil
	}
	call := in.Call
	call.HookEvent = in.Event
	call.HookName = in.Event
	if in.MatchQuery != "" {
		call.HookName += ":" + in.MatchQuery
	}
	call.InputFields = in.Fields.Clone()
	input, err := b.Input(ctx, call)
	if err != nil {
		return nil, err
	}
	if call.WorktreeWrapper {
		input.Delete("permission_mode")
	}
	futures := []*ownerasync.OwnerFuture[OutsideResult]{}
	for index, hook := range ordered {
		future, e := ownerasync.NewOwnerFuture[OutsideResult](b.Owner)
		if e != nil {
			return nil, e
		}
		futures = append(futures, future)
		_, e = b.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
			current := call
			current.Scope = scope
			current.HookIndex = &index
			current.HookID = s.options.UUID()
			value, failure := s.executeOutsideOne(ctx, current, hook, input, in.Timeout)
			if failure != nil {
				return future.Reject(scope, failure)
			}
			return future.Resolve(scope, value)
		})
		if e != nil {
			return nil, e
		}
	}
	results := make([]OutsideResult, 0, len(futures))
	for _, future := range futures {
		result, e := future.Await(call.Scope)
		if e != nil {
			return nil, e
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) executeOutsideOne(ctx context.Context, call Context, hook, input protocol.JSONObject, timeout time.Duration) (OutsideResult, error) {
	kind := hook.String("type")
	command := hook.Get("command")
	if kind == "prompt" || kind == "agent" {
		command = hook.Get("prompt")
	}
	if kind == "http" {
		command = hook.Get("url")
	}
	value := OutsideResult{Command: command, Output: commandJSON("")}
	failed := func(text json.RawMessage) (OutsideResult, error) { value.Output = text; return value, nil }
	if kind == "callback" {
		value.Command = commandJSON("callback")
		if !s.HasNativeCallbackBindings(s.options.Commands.Owner) {
			return value, unsupported("actual SDK callback transport")
		}
		active := ctx
		cancel := func() {}
		if timeout > 0 {
			duration, e := sdkCallbackTimeout(hook.Get("timeout"), timeout)
			if e != nil {
				return failed(commandJSON(e.Error()))
			}
			active, cancel = outsideCommandContext(ctx, duration)
		}
		defer cancel()
		result, e := s.options.Callbacks.Run(active, call, hook, input)
		if e != nil {
			return failed(commandJSON(e.Error()))
		}
		value.Succeeded = true
		if string(result.Get("async")) == "true" {
			return value, nil
		}
		specific, _ := result.Object("hookSpecificOutput")
		if call.HookEvent == "WorktreeCreate" && specific.String("hookEventName") == "WorktreeCreate" {
			value.Output = specific.Get("worktreePath")
		} else if hookTruthy(result.Get("systemMessage")) {
			value.Output = result.Get("systemMessage")
		}
		value.Blocked = result.String("decision") == "block"
		return value, nil
	}
	if kind == "prompt" {
		return failed(commandJSON("Prompt stop hooks are not yet supported outside REPL"))
	}
	if kind == "agent" {
		return failed(commandJSON("Agent stop hooks are not yet supported outside REPL"))
	}
	var parsed *protocol.JSONObject
	if kind == "http" {
		if !s.HasNativeHTTPBindings(s.options.Commands.Owner) {
			return value, unsupported("actual outside-REPL HTTP transport")
		}
		result, err := s.options.HTTP.Run(ctx, call, hook, input)
		if err != nil {
			var cap *CapabilityError
			if errors.As(err, &cap) {
				return value, err
			}
			return failed(commandJSON(err.Error()))
		}
		if result.Aborted {
			return failed(commandJSON("Hook cancelled"))
		}
		if hookTruthy(result.Error) || !result.OK {
			message := result.Error
			if !hookTruthy(message) {
				status := "undefined"
				if result.StatusCode != nil {
					status = fmt.Sprint(*result.StatusCode)
				}
				message = commandJSON("HTTP " + status + " from " + hook.String("url"))
			}
			return failed(message)
		}
		var validation json.RawMessage
		parsed, validation, err = parseHTTPHookOutput(result.Body)
		if err != nil {
			return value, err
		}
		if validation != nil {
			return failed(validation)
		}
		value.Succeeded = true
		value.Output = result.Body
		if inEvent := call.HookEvent; inEvent == "WorktreeCreate" {
			value.Output = commandJSON("")
			if parsed != nil {
				specific, _ := parsed.Object("hookSpecificOutput")
				if specific.String("hookEventName") == "WorktreeCreate" {
					value.Output = specific.Get("worktreePath")
				}
			}
		}
	} else {
		if kind != "command" {
			return value, unsupported("outside-REPL hook kind " + kind)
		}
		active := ctx
		cancel := func() {}
		if timeout > 0 {
			seconds := 0.0
			_ = json.Unmarshal(hook.Get("timeout"), &seconds)
			duration := timeout
			if seconds != 0 {
				duration = time.Duration(seconds*1000) * time.Millisecond
			}
			active, cancel = outsideCommandContext(ctx, duration)
		}
		defer cancel()
		output, err := s.options.Commands.Run(active, call, hook, input)
		if err != nil {
			var cap *CapabilityError
			if errors.As(err, &cap) {
				return value, err
			}
			return failed(commandJSON(err.Error()))
		}
		if output.Aborted {
			return failed(commandJSON("Hook cancelled"))
		}
		parsed, err = parseOutsideCommandOutput(output.Stdout)
		if err != nil {
			return failed(commandJSON(err.Error()))
		}
		value.Succeeded = output.Code == 0
		value.Blocked = output.Code == 2
		value.Output = commandJSON(output.Stderr)
		if value.Succeeded {
			value.Output = commandJSON(output.Stdout)
		}
		if parsed != nil && string(parsed.Get("async")) != "true" {
			specific, _ := parsed.Object("hookSpecificOutput")
			value.WatchPaths = specific.Get("watchPaths")
			value.SystemMessage = parsed.Get("systemMessage")
		}
	}
	if parsed != nil && string(parsed.Get("async")) != "true" && parsed.String("decision") == "block" {
		value.Blocked = true
	}
	return value, nil
}

func parseOutsideCommandOutput(stdout string) (*protocol.JSONObject, error) {
	trimmed := strings.TrimFunc(stdout, javaScriptWhitespace)
	if !strings.HasPrefix(trimmed, "{") || !json.Valid([]byte(trimmed)) {
		return nil, nil
	}
	schema, err := HookOutputSchema()
	if err != nil {
		return nil, err
	}
	parsed, issues := pluginschema.Validate(schema, []byte(trimmed))
	if len(issues) > 0 {
		decision, err := CommandStopDecision("", "SessionEnd", CommandOutput{Stdout: stdout})
		if err != nil {
			return nil, err
		}
		return nil, errors.New(decision.ValidationError)
	}
	var value protocol.JSONObject
	if err = value.UnmarshalJSON(parsed); err != nil {
		return nil, err
	}
	return &value, nil
}

// OutsideEventConfigured observes the same captured matcher list used by the
// outside-REPL executor, including an empty matcher group which may fail later.
func (s *Service) OutsideEventConfigured(event, sid string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.simple() {
		return false, nil
	}
	return s.exists(event, sid)
}
