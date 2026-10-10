package teamstore

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RuntimeSnapshot reads a team identity, never accepts a caller-provided copy
// of its membership. The raw DTO mirrors buildTeamRuntimeSnapshot.
func (c *Config) RuntimeSnapshot(name string, expectedCreatedAt json.RawMessage, session string) (protocol.JSONObject, error) {
	team := c.Read(name)
	var expected, actual float64
	if team == nil || json.Unmarshal(expectedCreatedAt, &expected) != nil || string(expectedCreatedAt) == "null" || json.Unmarshal(team.Get("createdAt"), &actual) != nil || expected != actual || team.String("leadSessionId") != session {
		return protocol.JSONObject{}, errors.New("Stale or foreign team runtime snapshot")
	}
	var members []protocol.JSONObject
	if err := json.Unmarshal(team.Get("members"), &members); err != nil {
		return protocol.JSONObject{}, err
	}
	teammates := protocol.NewJSONObject()
	for _, member := range members {
		kind := ""
		if member.String("backendType") == "process" {
			kind = "process"
		}
		teammates.Set(member.String("agentId"), raw(protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: member.Get("name")}, protocol.JSONField{Name: "agentType", Value: member.Get("agentType")}, protocol.JSONField{Name: "cwd", Value: member.Get("cwd")}, protocol.JSONField{Name: "tmuxSessionName", Value: raw(kind)}, protocol.JSONField{Name: "tmuxPaneId", Value: member.Get("tmuxPaneId")}, protocol.JSONField{Name: "spawnedAt", Value: member.Get("joinedAt")})))
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "teamName", Value: team.Get("name")}, protocol.JSONField{Name: "teamFilePath", Value: raw(c.Path(name))}, protocol.JSONField{Name: "leadAgentId", Value: team.Get("leadAgentId")}, protocol.JSONField{Name: "isLeader", Value: raw(true)}, protocol.JSONField{Name: "teammates", Value: raw(teammates)}), nil
}
