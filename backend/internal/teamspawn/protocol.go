package teamspawn

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ProtocolIdentity struct {
	TeamName, AgentName, AgentID, Color string
	PaneID, BackendType                 json.RawMessage
	Leader                              bool
	LeaderMode                          string
	Timestamp                           string
	Now                                 float64
	InProcessTaskFound                  bool
}
type ProtocolResult struct {
	Recipient               string
	Envelope, Output        protocol.JSONObject
	AbortSelf, GracefulExit bool
}

func StructuredMessage(to string, message protocol.JSONObject, identity ProtocolIdentity) (ProtocolResult, error) {
	var out ProtocolResult
	stamp := quoted(identity.Timestamp)
	name := identity.AgentName
	var body protocol.JSONObject
	color := optionalRaw(identity.Color)
	switch message.String("type") {
	case "shutdown_request":
		if name == "" {
			name = "team-lead"
		}
		id, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("shutdown-"), rawJSON(identity.Now), quoted("@" + to)}, "")
		if err != nil {
			return out, err
		}
		body = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("shutdown_request")}, protocol.JSONField{Name: "requestId", Value: id}, protocol.JSONField{Name: "from", Value: quoted(name)}, protocol.JSONField{Name: "reason", Value: message.Get("reason")}, protocol.JSONField{Name: "timestamp", Value: stamp})
		out.Recipient = to
		out.Output = protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: rawJSON(true)}, protocol.JSONField{Name: "message", Value: mustJoin([]json.RawMessage{quoted("Shutdown request sent to " + to + ". Request ID: "), id})}, protocol.JSONField{Name: "request_id", Value: id}, protocol.JSONField{Name: "target", Value: quoted(to)})
	case "shutdown_response":
		if name == "" {
			name = "teammate"
		}
		out.Recipient = "team-lead"
		id := message.Get("request_id")
		if string(message.Get("approve")) == "true" {
			body = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("shutdown_approved")}, protocol.JSONField{Name: "requestId", Value: id}, protocol.JSONField{Name: "from", Value: quoted(name)}, protocol.JSONField{Name: "timestamp", Value: stamp}, protocol.JSONField{Name: "paneId", Value: identity.PaneID}, protocol.JSONField{Name: "backendType", Value: identity.BackendType})
			out.AbortSelf = identity.InProcessTaskFound
			var backend string
			_ = json.Unmarshal(identity.BackendType, &backend)
			text := "Shutdown approved. Sent confirmation to team-lead. Agent " + name + " is now exiting."
			if backend != "in-process" {
				if identity.InProcessTaskFound {
					text = "Shutdown approved (fallback path). Agent " + name + " is now exiting."
				} else {
					out.GracefulExit = true
				}
			}
			out.Output = protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: rawJSON(true)}, protocol.JSONField{Name: "message", Value: quoted(text)}, protocol.JSONField{Name: "request_id", Value: id})
		} else {
			body = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("shutdown_rejected")}, protocol.JSONField{Name: "requestId", Value: id}, protocol.JSONField{Name: "from", Value: quoted(name)}, protocol.JSONField{Name: "reason", Value: message.Get("reason")}, protocol.JSONField{Name: "timestamp", Value: stamp})
			out.Output = protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: rawJSON(true)}, protocol.JSONField{Name: "message", Value: mustJoin([]json.RawMessage{quoted("Shutdown rejected. Reason: \""), message.Get("reason"), quoted("\". Continuing to work.")})}, protocol.JSONField{Name: "request_id", Value: id})
		}
	case "plan_approval_response":
		approved := string(message.Get("approve")) == "true"
		if !identity.Leader {
			if approved {
				return out, errors.New("Only the team lead can approve plans. Teammates cannot approve their own or other plans.")
			}
			return out, errors.New("Only the team lead can reject plans. Teammates cannot reject their own or other plans.")
		}
		name = "team-lead"
		color = nil
		out.Recipient = to
		id := message.Get("request_id")
		body = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("plan_approval_response")}, protocol.JSONField{Name: "requestId", Value: id}, protocol.JSONField{Name: "approved", Value: rawJSON(approved)})
		if approved {
			mode := identity.LeaderMode
			if mode == "plan" {
				mode = "default"
			}
			body.Set("timestamp", stamp)
			body.Set("permissionMode", quoted(mode))
			out.Output = protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: rawJSON(true)}, protocol.JSONField{Name: "message", Value: quoted("Plan approved for " + to + ". They will receive the approval and can proceed with implementation.")}, protocol.JSONField{Name: "request_id", Value: id})
		} else {
			feedback := message.Get("feedback")
			if feedback == nil || string(feedback) == "null" {
				feedback = quoted("Plan needs revision")
			}
			body.Set("feedback", feedback)
			body.Set("timestamp", stamp)
			out.Output = protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: rawJSON(true)}, protocol.JSONField{Name: "message", Value: mustJoin([]json.RawMessage{quoted("Plan rejected for " + to + " with feedback: \""), feedback, quoted("\"")})}, protocol.JSONField{Name: "request_id", Value: id})
		}
	default:
		return out, errors.New("Invalid original SendMessage structured type")
	}
	encoded := protocol.StringifyValidatedJavaScriptJSON(rawJSON(body))
	out.Envelope = protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoted(name)}, protocol.JSONField{Name: "text", Value: quoted(string(encoded))}, protocol.JSONField{Name: "timestamp", Value: stamp}, protocol.JSONField{Name: "color", Value: color})
	return out, nil
}

func mustJoin(parts []json.RawMessage) json.RawMessage {
	value, err := protocol.JoinJavaScriptValuesJSON(parts, "")
	if err != nil {
		panic(err)
	}
	return value
}
