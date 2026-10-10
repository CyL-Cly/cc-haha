package clibootstrap

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// Source throttles reminders by human turns. Tool feedback and meta users do
// not advance this clock even though attachments are checked each tool round.
func planAttachmentCadence(history []queryloop.Message) (turns int, found bool, count int) {
	for i := len(history) - 1; i >= 0; i-- {
		record := history[i].Record
		if history[i].Type() == "user" && !planRawTruthy(record.Get("isMeta")) && !planToolFeedback(record.Get("message")) {
			turns++
		} else if history[i].Type() == "attachment" {
			var payload protocol.JSONObject
			_ = payload.UnmarshalJSON(record.Get("attachment"))
			if payload.String("type") == "plan_mode" || payload.String("type") == "plan_mode_reentry" {
				found = true
				break
			}
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Type() != "attachment" {
			continue
		}
		var payload protocol.JSONObject
		_ = payload.UnmarshalJSON(history[i].Record.Get("attachment"))
		if payload.String("type") == "plan_mode_exit" {
			break
		}
		if payload.String("type") == "plan_mode" {
			count++
		}
	}
	return
}
func planRawTruthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	default:
		return true
	}
}
func planToolFeedback(raw json.RawMessage) bool {
	var api protocol.JSONObject
	if api.UnmarshalJSON(raw) != nil {
		return false
	}
	var blocks []protocol.JSONObject
	if json.Unmarshal(api.Get("content"), &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.String("type") == "tool_result" && len(block.Get("tool_use_id")) > 0 && block.Get("tool_use_id")[0] == '"' {
			return true
		}
	}
	return false
}
func (m *MainPrint) nativePlanAttachments(scope *cooperative.Scope, history []queryloop.Message, toolContexts ...*builtinwrite.CallContext) ([]queryloop.Message, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.plans == nil {
		return nil, nil
	}
	agentID := m.options.AgentID
	if m.teamWorker != nil {
		agentID = ""
	}
	var permission protocol.JSONObject
	var e error
	if len(toolContexts) > 0 && toolContexts[0] != nil {
		call := toolContexts[0]
		agentID = call.AgentID
		if call.PermissionContext == nil {
			return nil, cooperative.ErrRestricted
		}
		permission, e = call.PermissionContext(scope)
	} else {
		permission, e = m.nativeToolPermissionContext(scope)
	}
	if e != nil {
		return nil, e
	}
	mode := permission.String("mode")
	hasExited, needsExit, e := m.state.PlanModeFlags(scope)
	if e != nil {
		return nil, e
	}
	out := []queryloop.Message{}
	create := func(payload protocol.JSONObject) {
		data, _ := payload.MarshalJSON()
		out = append(out, queryloop.Message{Record: m.factory.CreateAttachment(data)})
	}
	if mode == "plan" {
		turns, found, count := planAttachmentCadence(history)
		if !found || turns >= 5 {
			plan, path, e := m.plans.Read(scope, agentID)
			if e != nil {
				return nil, e
			}
			exists := string(plan) != "null"
			if hasExited && exists {
				create(protocol.NewJSONObject(fieldMain("type", "plan_mode_reentry"), fieldMain("planFilePath", path)))
				if e := m.state.SetHasExitedPlanMode(scope, false); e != nil {
					return nil, e
				}
			}
			reminder := "sparse"
			if (count+1)%5 == 1 {
				reminder = "full"
			}
			create(protocol.NewJSONObject(fieldMain("type", "plan_mode"), fieldMain("reminderType", reminder), fieldMain("isSubAgent", agentID != ""), fieldMain("planFilePath", path), fieldMain("planExists", exists)))
		}
	}
	if needsExit {
		// Source clears the once flag even when a mode change returns to plan before
		// the pending notification is consumed.
		if e := m.state.SetNeedsPlanModeExitAttachment(scope, false); e != nil {
			return nil, e
		}
		if mode != "plan" {
			plan, path, e := m.plans.Read(scope, agentID)
			if e != nil {
				return nil, e
			}
			create(protocol.NewJSONObject(fieldMain("type", "plan_mode_exit"), fieldMain("planFilePath", path), fieldMain("planExists", string(plan) != "null")))
		}
	}
	return out, nil
}
func fieldMain(name string, value any) protocol.JSONField {
	data, _ := json.Marshal(value)
	return protocol.JSONField{Name: name, Value: data}
}

func (m *MainPrint) nativePlanProjection(scope *cooperative.Scope) (*attachments.PlanOptions, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.plans == nil {
		return nil, nil
	}
	interview, err := m.PlanInterviewPhase()
	if err != nil {
		return nil, err
	}
	n := m.options.Native
	if n.Environment["USER_TYPE"] == "ant" {
		return nil, mainCap("internal planning experiment and embedded search producers")
	}
	key := usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, filepath.EvalSymlinks, n.CWD)
	if key == "" {
		key = n.CWD
	}
	if m.options.Platform == "windows" {
		key = strings.ReplaceAll(key, "\\", "/")
	}
	project, err := m.Global.GetProject(key)
	if err != nil {
		return nil, err
	}
	var allowed []string
	if field := project.Get("allowedTools"); field != nil && string(field) != "null" {
		if err := json.Unmarshal(field, &allowed); err != nil {
			return nil, err
		}
	}
	// MainPrint's actual NewDirectCredentials selector suppresses subscription
	// identity. Its admitted external build has no embedded-tools or experiment
	// override producer; these are the original external release defaults.
	return &attachments.PlanOptions{MapToolResult: nativeAttachmentToolResult, Environment: map[string]string{"CLAUDE_CODE_PLAN_V2_AGENT_COUNT": n.Environment["CLAUDE_CODE_PLAN_V2_AGENT_COUNT"], "CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT": n.Environment["CLAUDE_CODE_PLAN_V2_EXPLORE_AGENT_COUNT"]}, InterviewPhase: interview, ExplorePlanAgentsEnabled: true, ProjectAllowedTools: allowed}, nil
}

func nativeAttachmentToolResult(name string, data, id json.RawMessage) (protocol.JSONObject, error) {
	result, e := builtintools.MapResult(name, data, id)
	var native *builtintools.CapabilityError
	if errors.As(e, &native) {
		return protocol.JSONObject{}, &attachments.CapabilityError{Boundary: native.Boundary}
	}
	return result, e
}
