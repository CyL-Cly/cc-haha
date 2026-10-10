package imruntime

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type PermissionDecision struct {
	RequestID string `json:"requestId"`
	Allowed   bool   `json:"allowed"`
	Rule      string `json:"rule,omitempty"`
}

func ParsePermissionCommand(text string, pending []string) *PermissionDecision {
	trimmed := protocol.TrimJavaScriptString(text)
	words := strings.FieldsFunc(trimmed, protocol.IsJavaScriptWhitespace)
	if len(words) >= 2 {
		action := strings.ToLower(words[0])
		if action == "/allow" || action == "/always" || action == "/allow-always" || action == "/deny" {
			decision := &PermissionDecision{RequestID: words[1], Allowed: action != "/deny"}
			if action == "/always" || action == "/allow-always" {
				decision.Rule = "always"
			}
			return decision
		}
	}
	if len(pending) != 1 {
		return nil
	}
	decision := &PermissionDecision{RequestID: pending[0]}
	switch strings.ToLower(trimmed) {
	case "1", "/1", "allow", "/allow", "y", "yes", "允许", "允许一次", "同意", "批准":
		decision.Allowed = true
	case "2", "/2", "always", "/always", "allow-always", "/allow-always", "永久允许", "一直允许":
		decision.Allowed = true
		decision.Rule = "always"
	case "3", "/3", "deny", "/deny", "n", "no", "拒绝", "不允许", "否":
	default:
		return nil
	}
	return decision
}
func ParsePermitCallback(data string) *PermissionDecision {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != "permit" || parts[1] == "" {
		return nil
	}
	decision := &PermissionDecision{RequestID: parts[1]}
	switch parts[2] {
	case "yes":
		decision.Allowed = true
	case "always":
		decision.Allowed = true
		decision.Rule = "always"
	case "no":
	default:
		return nil
	}
	return decision
}
func PermissionInstructions(id string) string {
	return "回复 1 允许一次，2 永久允许，3 拒绝。\n也可回复 /allow " + id + "、/always " + id + "、/deny " + id + "。"
}
func PermissionDecisionStatus(decision PermissionDecision) string {
	if !decision.Allowed {
		return "❌ 已拒绝"
	}
	if decision.Rule == "always" {
		return "♾️ 已永久允许"
	}
	return "✅ 已允许"
}

type ChatState struct {
	State, Verb, Model     string
	PendingPermissionCount int
	Pending                []string
}

func (state *ChatState) SyncPermission(message protocol.JSONObject) bool {
	remove := func(id string) {
		for i, v := range state.Pending {
			if v == id {
				state.Pending = append(state.Pending[:i], state.Pending[i+1:]...)
				break
			}
		}
	}
	switch message.String("type") {
	case "permission_resolved":
		if message.String("permissionType") != "tool" || len(message.Get("requestId")) == 0 || message.Get("requestId")[0] != '"' {
			return true
		}
		remove(message.String("requestId"))
		state.PendingPermissionCount = len(state.Pending)
		if len(state.Pending) == 0 && state.State == "permission_pending" {
			state.State = "thinking"
		}
		return true
	case "permission_requests_snapshot":
		values := message.Get("toolRequestIds")
		if len(values) > 0 && values[0] == '[' {
			state.Pending = []string{}
			var requests []json.RawMessage
			_ = json.Unmarshal(values, &requests)
			for _, request := range requests {
				if len(request) == 0 || request[0] != '"' {
					continue
				}
				id := TextJSON(request).String()
				if id == "" {
					continue
				}
				found := false
				for _, old := range state.Pending {
					found = found || old == id
				}
				if !found {
					state.Pending = append(state.Pending, id)
				}
			}
		}
		state.PendingPermissionCount = len(state.Pending)
		if len(state.Pending) > 0 {
			state.State = "permission_pending"
		} else if !truthy(message.Get("turnActive")) {
			state.State = "idle"
		} else if state.State == "idle" || state.State == "permission_pending" {
			state.State = "thinking"
		}
		return true
	}
	return false
}
