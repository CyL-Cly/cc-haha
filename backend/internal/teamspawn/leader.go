package teamspawn

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

type LeadMailboxPartition struct {
	PermissionRequests        []protocol.JSONObject `json:"permissionRequests"`
	SandboxPermissionRequests []protocol.JSONObject `json:"sandboxPermissionRequests"`
	Remaining                 []protocol.JSONObject `json:"remaining"`
}

// PartitionLeadMailboxMessages follows the original permissive type checks;
// permission requests are routed to the host and never to the model as chat.
func PartitionLeadMailboxMessages(messages []protocol.JSONObject) LeadMailboxPartition {
	result := LeadMailboxPartition{[]protocol.JSONObject{}, []protocol.JSONObject{}, []protocol.JSONObject{}}
	for _, message := range messages {
		parsed := inboxObject([]byte(message.String("text")))
		request := protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: rawJSON(message)}, protocol.JSONField{Name: "parsed", Value: rawJSON(parsed)})
		switch parsed.String("type") {
		case "permission_request":
			result.PermissionRequests = append(result.PermissionRequests, request)
		case "sandbox_permission_request":
			result.SandboxPermissionRequests = append(result.SandboxPermissionRequests, request)
		default:
			result.Remaining = append(result.Remaining, message.Clone())
		}
	}
	return result
}

func ShutdownApproved(message string) *protocol.JSONObject {
	parsed := inboxObject([]byte(message))
	if parsed.String("type") != "shutdown_approved" || parsed.String("requestId") == "" || parsed.String("from") == "" {
		return nil
	}
	var stamp string
	if value := parsed.Get("timestamp"); len(value) == 0 || value[0] != '"' || json.Unmarshal(value, &stamp) != nil {
		return nil
	}
	for _, field := range parsed.OwnProperties() {
		var key string
		_ = json.Unmarshal(field.NameJSON, &key)
		switch key {
		case "type", "requestId", "from", "timestamp":
		case "paneId":
			var value string
			if len(field.Value) == 0 || field.Value[0] != '"' || json.Unmarshal(field.Value, &value) != nil {
				return nil
			}
		case "backendType":
			value := parsed.String(key)
			if value != "tmux" && value != "iterm2" && value != "in-process" {
				return nil
			}
		default:
			return nil
		}
	}
	return &parsed
}

func FormatHeadlessTeammateMessages(messages []protocol.JSONObject) (json.RawMessage, error) {
	rows := make([]json.RawMessage, len(messages))
	for i, message := range messages {
		if _, e := protocol.DecodeJavaScriptJSONString(message.Get("from")); e != nil {
			return nil, errors.New("Teammate XML sender must be a string")
		}
		from, err := teamstore.EscapeXMLAttrJSON(message.Get("from"))
		if err != nil {
			return nil, err
		}
		text, err := protocol.ExactJavaScriptStringJSON(message.Get("text"))
		if err != nil {
			return nil, err
		}
		pieces := []json.RawMessage{quoted(`<teammate-message teammate_id="`), from, quoted(`"`)}
		if leaderTruthy(message.Get("color")) {
			if _, e := protocol.DecodeJavaScriptJSONString(message.Get("color")); e != nil {
				return nil, errors.New("Teammate XML color must be a string")
			}
			color, err := teamstore.EscapeXMLAttrJSON(message.Get("color"))
			if err != nil {
				return nil, err
			}
			pieces = append(pieces, quoted(` color="`), color, quoted(`"`))
		}
		if leaderTruthy(message.Get("summary")) {
			if _, e := protocol.DecodeJavaScriptJSONString(message.Get("summary")); e != nil {
				return nil, errors.New("Teammate XML summary must be a string")
			}
			summary, e := teamstore.EscapeXMLAttrJSON(message.Get("summary"))
			if e != nil {
				return nil, e
			}
			pieces = append(pieces, quoted(` summary="`), summary, quoted(`"`))
		}
		pieces = append(pieces, quoted(">\n"), text, quoted("\n</teammate-message>"))
		rows[i], err = protocol.JoinJavaScriptValuesJSON(pieces, "")
		if err != nil {
			return nil, err
		}
	}
	return protocol.JoinJavaScriptValuesJSON(rows, "\n\n")
}

func leaderTruthy(value json.RawMessage) bool {
	if len(value) == 0 || string(value) == "null" || string(value) == "false" || string(value) == `""` {
		return false
	}
	var n float64
	if json.Unmarshal(value, &n) == nil {
		return n != 0
	}
	return true
}

const ShutdownTeamPrompt = `<system-reminder>
You are running in non-interactive mode and cannot return a response to the user until your team is shut down.

You MUST shut down your team before preparing your final response:
1. Use requestShutdown to ask each team member to shut down gracefully
2. Wait for shutdown approvals
3. Use the cleanup operation to clean up the team
4. Only then provide your final response to the user

The user cannot receive your response until the team is completely shut down.
</system-reminder>

Shut down your team and prepare your final response for the user.`
