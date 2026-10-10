package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/httphook"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type HTTPBindings struct {
	Owner *cooperative.Owner
	Run   func(context.Context, Context, protocol.JSONObject, protocol.JSONObject) (httphook.Result, error)
}

func (s *Service) HasNativeHTTPBindings(owner *cooperative.Owner) bool {
	return s != nil && s.options.HTTP != nil && s.options.HTTP.Owner == owner && s.options.HTTP.Run != nil && s.HasNativeCommandBindings(owner)
}

type nativeHTTPCompletion struct {
	Event           EventResult
	Response        CommandOutput
	ResponseOutcome string
}

func parseHTTPHookOutput(body json.RawMessage) (*protocol.JSONObject, json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(body)
	if err != nil {
		return nil, nil, err
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	text := string(utf16.Decode(units))
	if len(units) == 0 {
		text = "{}"
	} else if units[0] != '{' {
		preview := units
		if len(preview) > 200 {
			preview = append(append([]uint16{}, preview[:200]...), 0x2026)
		}
		prefix := utf16.Encode([]rune("HTTP hook must return JSON, but got non-JSON response body: "))
		return nil, protocol.QuoteJavaScriptUTF16(append(prefix, preview...)), nil
	}
	if message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(text); invalid {
		return nil, commandStringConcat("HTTP hook must return valid JSON, but parsing failed: SyntaxError: ", message), nil
	}
	schema, err := HookOutputSchema()
	if err != nil {
		return nil, nil, err
	}
	parsed, issues := pluginschema.Validate(schema, []byte(text))
	if len(issues) > 0 {
		lines := []string{}
		for _, issue := range issues {
			parts := []string{}
			for _, part := range issue.Path {
				parts = append(parts, fmt.Sprint(part))
			}
			lines = append(lines, "  - "+strings.Join(parts, ".")+": "+issue.Message)
		}
		return nil, commandJSON("Hook JSON output validation failed:\n" + strings.Join(lines, "\n") + "\n\nThe hook's output was: " + prettyHookJSON([]byte(text))), nil
	}
	var value protocol.JSONObject
	if err = value.UnmarshalJSON(parsed); err != nil {
		return nil, nil, err
	}
	return &value, nil, nil
}

// HTTP responses use the original HTTP-specific parser and omit command
// timing fields except when the surrounding executor itself throws.
func (s *Service) nativeHTTPEventCompletion(call Context, hook protocol.JSONObject, id string, value httphook.Result, failure error, duration float64) (nativeHTTPCompletion, error) {
	fields := protocol.NewJSONObject()
	attachment := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("hook_success")}, protocol.JSONField{Name: "hookName", Value: commandJSON(call.HookName)}, protocol.JSONField{Name: "toolUseID", Value: commandJSON(id)}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(call.HookEvent)})
	completion := nativeHTTPCompletion{Event: EventResult{Fields: fields, Outcome: "success", Hook: hook.Clone()}, Response: CommandOutput{CodeAbsent: value.StatusCode == nil}, ResponseOutcome: "success"}
	if value.StatusCode != nil {
		completion.Response.Code = *value.StatusCode
	}
	finish := func() {
		message := queryloop.Message{Record: s.options.Commands.Factory.CreateAttachment(commandJSON(attachment))}
		completion.Event.Message = &message
	}
	failed := func(stderr json.RawMessage, stdout json.RawMessage, code int) {
		attachment.Set("type", commandJSON("hook_non_blocking_error"))
		attachment.Set("stderr", stderr)
		attachment.Set("stdout", stdout)
		attachment.Set("exitCode", commandJSON(code))
		completion.Event.Outcome = "non_blocking_error"
		completion.ResponseOutcome = "error"
		completion.Response.Stderr = attachment.String("stderr")
		completion.Response.Stdout = attachment.String("stdout")
	}
	if failure != nil {
		var capability *httphook.CapabilityError
		var ownCapability *CapabilityError
		if errors.As(failure, &capability) || errors.As(failure, &ownCapability) {
			return completion, failure
		}
		stderr := commandJSON("Failed to run: " + failure.Error())
		failed(stderr, commandJSON(""), 1)
		attachment.Set("command", commandJSON(nativeEventHookDisplay(hook)))
		attachment.Set("durationMs", commandJSON(duration))
		completion.Response.Code = 1
		completion.Response.CodeAbsent = false
		completion.Response.Output = attachment.String("stderr")
		finish()
		return completion, nil
	}
	if value.Aborted {
		attachment.Set("type", commandJSON("hook_cancelled"))
		completion.Event.Outcome = "cancelled"
		completion.ResponseOutcome = "cancelled"
		completion.Response.Output = "Hook cancelled"
		completion.Response.CodeAbsent = true
		finish()
		return completion, nil
	}
	if value.Error != nil && hookTruthy(value.Error) || !value.OK {
		stderr := value.Error
		if !hookTruthy(stderr) {
			status := "undefined"
			if value.StatusCode != nil {
				status = fmt.Sprint(*value.StatusCode)
			}
			stderr = commandJSON("HTTP " + status + " from " + hook.String("url"))
		}
		failed(stderr, commandJSON(""), completion.Response.Code)
		completion.Response.Output = attachment.String("stderr")
		finish()
		return completion, nil
	}
	parsed, validation, err := parseHTTPHookOutput(value.Body)
	if err != nil {
		return completion, err
	}
	if validation != nil {
		failed(commandStringConcat("JSON validation failed: ", validation), value.Body, completion.Response.Code)
		completion.Response.Output = stringFromJSON(value.Body)
		finish()
		return completion, nil
	}
	completion.Response.Stdout = stringFromJSON(value.Body)
	completion.Response.Output = completion.Response.Stdout
	if parsed != nil && string(parsed.Get("async")) == "true" {
		return completion, nil
	}
	if parsed != nil {
		stdout, stderr := completion.Response.Stdout, ""
		interpreted, err := InterpretJSONOutput(*parsed, JSONOutputContext{Command: hook.String("url"), HookName: call.HookName, ToolUseID: id, Event: call.HookEvent, ExpectedEvent: call.HookEvent, Stdout: &stdout, Stderr: &stderr, ExitCode: value.StatusCode})
		if err != nil {
			return s.nativeHTTPEventCompletion(call, hook, id, httphook.Result{}, err, duration)
		}
		completion.Event.Fields = interpreted.Fields
		attachment = interpreted.Attachment
		finish()
	}
	return completion, nil
}

func stringFromJSON(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
