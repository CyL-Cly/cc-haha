package builtintools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

var localAgentAddress = regexp.MustCompile(`^a(?:.+-)?[0-9a-f]{16}$`)

func (s *Service) validateSendMessage(in protocol.JSONObject) error {
	if protocol.TrimJavaScriptString(in.String("to")) == "" {
		return fmt.Errorf("to must not be empty")
	}
	if strings.Contains(in.String("to"), "@") {
		return fmt.Errorf("to must be a bare teammate name or \"*\" — there is only one team per session")
	}
	if value := in.Get("message"); len(value) > 0 && value[0] == '"' {
		var structured protocol.JSONObject
		if structured.UnmarshalJSON([]byte(in.String("message"))) == nil {
			switch structured.String("type") {
			case "permission_request", "permission_response", "sandbox_permission_request", "sandbox_permission_response", "shutdown_request", "shutdown_approved", "team_permission_update", "mode_set_request", "plan_approval_request", "plan_approval_response":
				return fmt.Errorf("reserved team protocol messages must use the structured message path")
			}
		}
		if protocol.TrimJavaScriptString(in.String("summary")) == "" {
			return fmt.Errorf("summary is required when message is a string")
		}
		return nil
	}
	if in.String("to") == "*" {
		return fmt.Errorf("structured messages cannot be broadcast (to: \"*\")")
	}
	message, _ := in.Object("message")
	if message.String("type") == "shutdown_response" {
		if in.String("to") != "team-lead" {
			return fmt.Errorf("shutdown_response must be sent to \"team-lead\"")
		}
		if string(message.Get("approve")) != "true" && protocol.TrimJavaScriptString(message.String("reason")) == "" {
			return fmt.Errorf("reason is required when rejecting a shutdown request")
		}
	}
	if s.options.TeamMessage == nil {
		return capability("SendMessage teammate structured protocol lifecycle")
	}
	return nil
}

func (s *Service) callSendMessage(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return queryloop.ToolCallResult{}, e
	}
	if e := s.validateSendMessage(in); e != nil {
		return queryloop.ToolCallResult{}, e
	}
	if _, structured := in.Object("message"); structured {
		if s.options.TeamMessage == nil {
			return queryloop.ToolCallResult{}, capability("SendMessage teammate structured protocol lifecycle")
		}
		future, err := s.options.TeamMessage(ctx, scope, in.Clone())
		if err != nil {
			return queryloop.ToolCallResult{}, err
		}
		return future.Await(scope)
	}
	b := s.options.BackgroundTasks
	if b == nil || b.ResolveName == nil || s.options.ResumeAgent == nil {
		return queryloop.ToolCallResult{}, capability("SendMessage actual Agent continuation producer")
	}
	to := in.String("to")
	id, e := b.ResolveName(scope, to)
	if e != nil {
		return queryloop.ToolCallResult{}, e
	}
	if id == "" && localAgentAddress.MatchString(to) {
		id = to
	}
	if id == "" {
		if s.options.TeamMessage != nil {
			future, err := s.options.TeamMessage(ctx, scope, in.Clone())
			if err != nil {
				return queryloop.ToolCallResult{}, err
			}
			return future.Await(scope)
		}
		return queryloop.ToolCallResult{}, capability("SendMessage teammate mailbox/broadcast lifecycle")
	}
	task, e := b.Lookup(scope, id)
	if e != nil {
		return queryloop.ToolCallResult{}, e
	}
	message := ""
	success := true
	if task != nil && task.Data.String("type") == "local_agent" && task.Data.String("agentType") != "main-session" {
		status := task.Data.String("status")
		if status == "running" {
			if e = QueueAgentPendingMessageJSON(b, scope, id, in.Get("message")); e != nil {
				return queryloop.ToolCallResult{}, e
			}
			message = fmt.Sprintf("Message queued for delivery to %s at its next tool round.", to)
		} else {
			output, failure := s.options.ResumeAgent(ctx, scope, id, in.String("message"))
			if failure != nil {
				success = false
				message = fmt.Sprintf("Agent \"%s\" is stopped (%s) and could not be resumed: %s", to, status, failure.Error())
			} else {
				message = fmt.Sprintf("Agent \"%s\" was stopped (%s); resumed it in the background with your message. You'll be notified when it finishes. Output: %s", to, status, output)
			}
		}
	} else {
		output, failure := s.options.ResumeAgent(ctx, scope, id, in.String("message"))
		if failure != nil {
			success = false
			message = fmt.Sprintf("Agent \"%s\" is registered but has no transcript to resume. It may have been cleaned up. (%s)", to, failure.Error())
		} else {
			message = fmt.Sprintf("Agent \"%s\" had no active task; resumed from transcript in the background with your message. You'll be notified when it finishes. Output: %s", to, output)
		}
	}
	return queryloop.ToolCallResult{Data: raw(protocol.NewJSONObject(field("success", success), field("message", message)))}, nil
}

func sendMessageBackfill(in *protocol.JSONObject) error {
	if in.Get("type") != nil {
		return nil
	}
	to := in.String("to")
	if len(in.Get("message")) > 0 && in.Get("message")[0] == '"' {
		if to == "*" {
			in.Set("type", quote("broadcast"))
		} else {
			in.Set("type", quote("message"))
			in.Set("recipient", in.Get("to"))
		}
		in.Set("content", in.Get("message"))
	} else {
		var msg protocol.JSONObject
		_ = json.Unmarshal(in.Get("message"), &msg)
		in.Set("type", msg.Get("type"))
		in.Set("recipient", in.Get("to"))
		for _, key := range []string{"request_id", "approve"} {
			if v := msg.Get(key); v != nil {
				in.Set(key, v)
			}
		}
		value := msg.Get("reason")
		if value == nil || string(value) == "null" {
			value = msg.Get("feedback")
		}
		if value != nil {
			in.Set("content", value)
		}
	}
	return nil
}

func (s *Service) sendMessageEnabled(scope *cooperative.Scope) (bool, error) {
	if s.options.SendMessageEnabled != nil {
		return s.options.SendMessageEnabled(scope)
	}
	return false, nil
}
