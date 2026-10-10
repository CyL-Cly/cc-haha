package teamspawn

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"sync"
)

const HeadlessTeammatePermissionUnavailable = "Team lead cannot prompt for teammate tool permission in this session."

type LeaderPermissionBridge struct {
	Mailbox       *teamstore.Mailbox
	TeamName      string
	CanPromptHost bool
	Ask           func(context.Context, protocol.JSONObject) (protocol.JSONObject, error)
	Timestamp     func() string
	Log           func(error)
}

// Resolve uses the existing host permission request and the original mailbox
// responses. Source concurrently settles each independent request, and catches
// host failure as a denial instead of sending a permission payload to the model.
func (b LeaderPermissionBridge) Resolve(ctx context.Context, partition LeadMailboxPartition) error {
	if b.Mailbox == nil || b.Timestamp == nil || b.CanPromptHost && b.Ask == nil {
		return errors.New("leader permissions require original host and mailbox producers")
	}
	var wg sync.WaitGroup
	for _, entry := range partition.PermissionRequests {
		entry := entry.Clone()
		wg.Add(1)
		go func() { defer wg.Done(); b.resolveTool(ctx, entry) }()
	}
	for _, entry := range partition.SandboxPermissionRequests {
		entry := entry.Clone()
		wg.Add(1)
		go func() { defer wg.Done(); b.resolveSandbox(ctx, entry) }()
	}
	wg.Wait()
	return nil
}
func (b LeaderPermissionBridge) write(name string, message protocol.JSONObject) {
	if b.TeamName == "" {
		return
	}
	envelope := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: quoted("team-lead")}, protocol.JSONField{Name: "text", Value: quoted(string(protocol.StringifyValidatedJavaScriptJSON(rawJSON(message))))}, protocol.JSONField{Name: "timestamp", Value: quoted(b.Timestamp())})
	if err := b.Mailbox.Write(name, b.TeamName, envelope); err != nil && b.Log != nil {
		b.Log(err)
	}
}
func (b LeaderPermissionBridge) resolveTool(ctx context.Context, entry protocol.JSONObject) {
	parsed, _ := entry.Object("parsed")
	request := protocol.NewJSONObject(protocol.JSONField{Name: "toolName", Value: parsed.Get("tool_name")}, protocol.JSONField{Name: "input", Value: parsed.Get("input")}, protocol.JSONField{Name: "toolUseId", Value: parsed.Get("tool_use_id")}, protocol.JSONField{Name: "description", Value: parsed.Get("description")}, protocol.JSONField{Name: "displayName", Value: parsed.Get("agent_id")}, protocol.JSONField{Name: "permissionSuggestions", Value: parsed.Get("permission_suggestions")})
	decision := protocol.NewJSONObject()
	feedback := HeadlessTeammatePermissionUnavailable
	if b.CanPromptHost {
		value, err := b.Ask(ctx, request)
		if err != nil {
			feedback = err.Error()
		} else {
			decision = value
			feedback = value.String("message")
			if feedback == "" {
				feedback = "Permission denied"
			}
		}
	}
	response := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("permission_response")}, protocol.JSONField{Name: "request_id", Value: parsed.Get("request_id")})
	if decision.String("behavior") == "allow" {
		response.Set("subtype", quoted("success"))
		result := protocol.NewJSONObject(protocol.JSONField{Name: "updated_input", Value: decision.Get("updatedInput")}, protocol.JSONField{Name: "permission_updates", Value: decision.Get("updatedPermissions")})
		response.Set("response", rawJSON(result))
	} else {
		response.Set("subtype", quoted("error"))
		response.Set("error", quoted(feedback))
	}
	b.write(parsed.String("agent_id"), response)
}
func (b LeaderPermissionBridge) resolveSandbox(ctx context.Context, entry protocol.JSONObject) {
	parsed, _ := entry.Object("parsed")
	pattern, _ := parsed.Object("hostPattern")
	host := pattern.Get("host")
	if !leaderTruthy(host) {
		return
	}
	allowed := false
	if b.CanPromptHost {
		name, err := protocol.ExactJavaScriptStringJSON(parsed.Get("workerName"))
		if err != nil {
			return
		}
		hostText, err := protocol.ExactJavaScriptStringJSON(host)
		if err != nil {
			return
		}
		description, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{name, quoted(" needs network access to "), hostText}, "")
		if err != nil {
			return
		}
		request := protocol.NewJSONObject(protocol.JSONField{Name: "toolName", Value: quoted("SandboxNetworkAccess")}, protocol.JSONField{Name: "input", Value: rawJSON(protocol.NewJSONObject(protocol.JSONField{Name: "host", Value: host}))}, protocol.JSONField{Name: "toolUseId", Value: parsed.Get("requestId")}, protocol.JSONField{Name: "description", Value: description}, protocol.JSONField{Name: "displayName", Value: parsed.Get("workerName")})
		decision, err := b.Ask(ctx, request)
		allowed = err == nil && decision.String("behavior") == "allow"
	}
	response := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("sandbox_permission_response")}, protocol.JSONField{Name: "requestId", Value: parsed.Get("requestId")}, protocol.JSONField{Name: "host", Value: host}, protocol.JSONField{Name: "allow", Value: rawJSON(allowed)}, protocol.JSONField{Name: "timestamp", Value: quoted(b.Timestamp())})
	b.write(parsed.String("workerName"), response)
}
