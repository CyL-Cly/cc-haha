package teamstore

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RemoveMember is the original existing-file mutation. A team that disappeared
// returns false; other persistence failures keep their real errors.
func (c *Config) RemoveMember(team, id, name string) (bool, error) {
	if id == "" && name == "" {
		return false, nil
	}
	changed := false
	_, err := c.Mutate(team, func(file protocol.JSONObject) (protocol.JSONObject, error) {
		var members []protocol.JSONObject
		if err := json.Unmarshal(file.Get("members"), &members); err != nil {
			return file, err
		}
		kept := make([]protocol.JSONObject, 0, len(members))
		for _, member := range members {
			if id != "" && member.String("agentId") == id || name != "" && member.String("name") == name {
				changed = true
				continue
			}
			kept = append(kept, member)
		}
		file.Set("members", raw(kept))
		return file, nil
	})
	var missing *TeamFileNotFoundError
	if errors.As(err, &missing) {
		return false, nil
	}
	return changed, err
}

// RemoveMemberByAgentID is Source's first-match splice used by the kill path.
// Shutdown approvals instead use RemoveMember, which filters both identifiers.
func (c *Config) RemoveMemberByAgentID(team, id string) (bool, error) {
	changed := false
	_, err := c.Mutate(team, func(file protocol.JSONObject) (protocol.JSONObject, error) {
		var members []protocol.JSONObject
		if err := json.Unmarshal(file.Get("members"), &members); err != nil {
			return file, err
		}
		for index, member := range members {
			if member.String("agentId") == id {
				members = append(members[:index:index], members[index+1:]...)
				file.Set("members", raw(members))
				changed = true
				break
			}
		}
		return file, nil
	})
	var missing *TeamFileNotFoundError
	if errors.As(err, &missing) {
		return false, nil
	}
	return changed, err
}
