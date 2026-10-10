package agenthook

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ToolAllowed uses the original external ALL_AGENT_DISALLOWED_TOOLS. The
// separate userType input retains the Source ant exception for nested Agent.
func ToolAllowed(name, userType string) bool {
	switch name {
	case "TaskOutput", "ExitPlanMode", "EnterPlanMode", "AskUserQuestion", "TaskStop", "Workflow", "StructuredOutput":
		return false
	case "Agent":
		return userType == "ant"
	}
	return true
}

// PermissionContext is the original child getAppState overlay. All maps and
// optional fields remain from the actual parent snapshot; the parent object
// and its session rule slice are never mutated or permanently granted.
func PermissionContext(parent protocol.JSONObject, transcript string) (protocol.JSONObject, error) {
	out := parent.Clone()
	allow, ok := out.Object("alwaysAllowRules")
	if !ok {
		return protocol.JSONObject{}, errors.New("Agent Hook requires actual parent permission rules")
	}
	// JSONObject.Clone is Source's shallow object spread. The original child
	// getter spreads alwaysAllowRules again before replacing its session array.
	allow = allow.Clone()
	rules := []json.RawMessage{}
	if data := allow.Get("session"); data != nil && string(data) != "null" {
		if e := json.Unmarshal(data, &rules); e != nil {
			return protocol.JSONObject{}, e
		}
	}
	rules = append(rules, jsonValue("Read(/"+transcript+")"))
	allow.Set("session", jsonValue(rules))
	out.Set("alwaysAllowRules", jsonValue(allow))
	out.Set("mode", jsonValue("dontAsk"))
	return out, nil
}
