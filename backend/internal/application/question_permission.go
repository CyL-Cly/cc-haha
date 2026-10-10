package application

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

const rejectUI = "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed."
const rejectUIReason = "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). To tell you how to proceed, the user said:\n"
const questionClarifyUI = "The user wants to clarify these questions.\n    This means they may have additional information, context or questions for you.\n    Take their response into account and then reformulate the questions if appropriate.\n    Start by asking them what they would like to clarify."
const questionClarifyUIPrefix = questionClarifyUI + "\n\n    Questions asked:\n"

func buildDesktopDenyMessage(tool, feedback string) string {
	feedback = strings.Trim(feedback, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
	switch tool {
	case "AskUserQuestion":
		if feedback != "" {
			return questionClarifyUIPrefix + feedback
		}
		return questionClarifyUI
	case "ExitPlanMode":
		if feedback != "" {
			return "The user rejected this plan and chose to stay in plan mode. Do not start implementing. Revise the plan to address this feedback and present it again for approval. The user said:\n" + feedback
		}
		return "The user rejected this plan and chose to stay in plan mode. Do not start implementing. Revise the plan and present it again for approval."
	default:
		if feedback != "" {
			return rejectUIReason + feedback
		}
		return rejectUI
	}
}

func desktopPermissionDecision(frame protocol.JSONObject, tool string) (tui.PermissionDecision, error) {
	decision := tui.PermissionDecision{Allowed: string(frame.Get("allowed")) == "true", UpdatedInput: frame.Get("updatedInput"), Rule: frame.Get("rule"), Message: frame.Get("message"), AcceptFeedback: frame.Get("acceptFeedback")}
	if !decision.Allowed && frame.String("behavior") == "ask" {
		decision.Behavior = "ask"
	}
	if decision.Allowed && tool == "AskUserQuestion" && decision.UpdatedInput != nil {
		updated := obj(decision.UpdatedInput)
		metadata := obj(updated.Get("metadata"))
		if metadata.IsObject() {
			metadata.Delete("autoAnswered")
			updated.Set("metadata", raw(metadata))
			decision.UpdatedInput = raw(updated)
		}
	}
	if !decision.Allowed && decision.Message == nil {
		feedback := ""
		if candidate := frame.Get("denyMessage"); candidate != nil {
			if json.Unmarshal(candidate, &feedback) != nil || string(candidate) == "null" {
				return decision, errors.New("permission denyMessage must be a string")
			}
		}
		decision.Message = raw(buildDesktopDenyMessage(tool, feedback))
	}
	if content := frame.Get("contentBlocks"); content != nil {
		if json.Unmarshal(content, &decision.ContentBlocks) != nil || string(content) == "null" {
			return decision, errors.New("permission contentBlocks must be an array")
		}
	}
	return decision, nil
}
