package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func userPromptContentText(content json.RawMessage) (json.RawMessage, error) {
	if _, err := protocol.DecodeJavaScriptJSONString(content); err == nil {
		return append(json.RawMessage(nil), content...), nil
	}
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, err
	}
	parts := []json.RawMessage{}
	for _, block := range blocks {
		if block.String("type") == "text" {
			parts = append(parts, block.Get("text"))
		}
	}
	joined, err := protocol.JoinJavaScriptValuesJSON(parts, "\n")
	if err != nil {
		return nil, err
	}
	units, err := protocol.DecodeJavaScriptJSONString(joined)
	if err != nil {
		return nil, err
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}

func truncateUserPromptHookText(content json.RawMessage) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(content)
	if err != nil {
		return nil, err
	}
	if len(units) <= 10000 {
		return append(json.RawMessage(nil), content...), nil
	}
	return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{protocol.QuoteJavaScriptUTF16(units[:10000]), quote("… [output truncated - exceeded 10000 characters]")}, "")
}

func (h *Host) processQueuedPrintInput(scope *cooperative.Scope, content, uuid json.RawMessage, skipSlash, meta bool, origin json.RawMessage) (*sessioncontroller.ProcessedInput, error) {
	return h.processNativeInputWithPromptHooks(scope, content, uuid, skipSlash, meta, origin, false)
}

func (h *Host) processNativeInputWithPromptHooks(scope *cooperative.Scope, content, uuid json.RawMessage, skipSlash, meta bool, origin json.RawMessage, skipAttachments bool) (*sessioncontroller.ProcessedInput, error) {
	if h.options.Print != nil && h.options.Print.NativeProcess {
		if err := h.acknowledgeNativeLocalDispatch(scope, content, uuid, skipSlash); err != nil {
			return nil, err
		}
	}
	var mode json.RawMessage
	if h.options.Print != nil && h.options.Print.NativeProcess {
		_, permission, err := h.options.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return nil, err
		}
		mode = append(json.RawMessage(nil), permission.Get("mode")...)
	}
	result, err := h.processQueuedPrintInputBase(scope, content, uuid, skipSlash, meta, origin)
	if err != nil || result == nil || result.LocalResult != nil || result.NoQuery || h.options.Print == nil || !h.options.Print.NativeProcess {
		return result, err
	}
	sid := h.subagentHookID()
	if sid == "" {
		sid = h.options.Controller.SessionID()
	}
	present, err := h.hooks.NativeEventPresent("UserPromptSubmit", sid)
	if err != nil || !present {
		return result, err
	}
	if h.inputModelContext == nil {
		return nil, cap("actual UserPromptSubmit abort lifetime")
	}
	previous := h.serial.History()
	if !result.AttachmentsHandled {
		if !skipAttachments {
			var attached []queryloop.Message
			if len(result.Messages) == 0 {
				return nil, cap("UserPromptSubmit original input-base messages")
			}
			text, present, failure := originalInputAttachmentText(&result.Messages[0])
			if failure != nil {
				return nil, failure
			}
			if present {
				if h.options.Print.InputAttachments != nil {
					attached, err = h.options.Print.InputAttachments(h.inputModelContext, scope, text, previous)
				} else if h.options.Print.PlanAttachments != nil {
					attached, err = h.options.Print.PlanAttachments(scope, previous)
				}
			}
			if err != nil {
				return nil, err
			}
			for _, message := range attached {
				if message.Type() != "attachment" {
					return nil, cap("UserPromptSubmit original initial attachments")
				}
				result.Messages = append(result.Messages, message.Record)
			}
		}
		result.AttachmentsHandled = true
	}
	prompt, err := userPromptContentText(content)
	if err != nil {
		return nil, err
	}
	call := queryhooks.Context{Scope: scope, History: previous, ToolUseContext: h, QuerySource: h.querySource(), AgentID: h.subagentHookID()}
	finished := false
	err = h.hooks.ExecuteUserPromptSubmitHooks(h.inputModelContext, queryhooks.NativeUserPromptInput{Prompt: prompt, PermissionMode: mode, Call: call}, func(value queryhooks.EventResult) error {
		if finished || value.Message != nil && value.Message.Type() == "progress" {
			return nil
		}
		fields := value.Fields
		if blocked, ok := fields.Object("blockingError"); ok {
			text, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("UserPromptSubmit operation blocked by hook:\n"), blocked.Get("blockingError"), quote("\n\nOriginal prompt: "), content}, "")
			if err != nil {
				return err
			}
			*result = sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{h.options.Factory.CreateSystemInformation(text, "warning")}, NoQuery: true, StartMillis: h.options.NowMillis(), CommandRules: result.CommandRules}
			finished = true
			return nil
		}
		if hookRawTruthy(fields.Get("preventContinuation")) {
			text := quote("Operation stopped by hook")
			if reason := fields.Get("stopReason"); hookRawTruthy(reason) {
				var err error
				text, err = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("Operation stopped by hook: "), reason}, "")
				if err != nil {
					return err
				}
			}
			result.Messages = append(result.Messages, messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": text})))
			result.NoQuery, result.StartMillis, finished = true, h.options.NowMillis(), true
			return nil
		}
		if contexts := fields.Get("additionalContexts"); contexts != nil {
			var contents []json.RawMessage
			if err := json.Unmarshal(contexts, &contents); err != nil {
				return err
			}
			if len(contents) > 0 {
				for i, content := range contents {
					var err error
					contents[i], err = truncateUserPromptHookText(content)
					if err != nil {
						return err
					}
				}
				attachment := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("hook_additional_context")}, protocol.JSONField{Name: "content", Value: raw(contents)}, protocol.JSONField{Name: "hookName", Value: quote("UserPromptSubmit")}, protocol.JSONField{Name: "toolUseID", Value: quote("hook-" + h.options.UUID())}, protocol.JSONField{Name: "hookEvent", Value: quote("UserPromptSubmit")})
				result.Messages = append(result.Messages, h.options.Factory.CreateAttachment(raw(attachment)))
			}
		}
		if value.Message != nil {
			message := value.Message.Record.Clone()
			attachment, ok := message.Object("attachment")
			if !ok {
				return cap("UserPromptSubmit original hook attachment")
			}
			if attachment.String("type") == "hook_success" {
				if !hookRawTruthy(attachment.Get("content")) {
					return nil
				}
				text, err := truncateUserPromptHookText(attachment.Get("content"))
				if err != nil {
					return err
				}
				attachment.Set("content", text)
				message.Set("attachment", raw(attachment))
			}
			result.Messages = append(result.Messages, message)
		}
		return nil
	})
	return result, err
}
