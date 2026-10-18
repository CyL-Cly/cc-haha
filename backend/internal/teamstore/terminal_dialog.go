package teamstore

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TeammateStatuses is the TeamsDialog projection of the existing config file.
// The leader exclusion, exact false active flag and optional field elision
// follow teamDiscovery; no runtime task roster is substituted for this file.
func (c *Config) TeammateStatuses(team string) ([]protocol.JSONObject, error) {
	result := []protocol.JSONObject{}
	file := c.Read(team)
	if file == nil {
		return result, nil
	}
	var members []protocol.JSONObject
	if err := json.Unmarshal(file.Get("members"), &members); err != nil {
		return nil, err
	}
	hidden := []json.RawMessage{}
	if value := file.Get("hiddenPaneIds"); len(value) > 0 && string(value) != "null" {
		if err := json.Unmarshal(value, &hidden); err != nil {
			return nil, err
		}
	}
	for _, member := range members {
		if member.String("name") == "team-lead" {
			continue
		}
		row := protocol.NewJSONObject()
		for _, name := range []string{"name", "agentId", "agentType", "prompt", "color", "tmuxPaneId", "cwd", "worktreePath", "mode"} {
			if value := member.Get(name); value != nil {
				row.Set(name, value)
			}
		}
		if truthy(member.Get("model")) {
			row.Set("model", member.Get("model"))
		}
		status := "running"
		if string(member.Get("isActive")) == "false" {
			status = "idle"
		}
		row.Set("status", raw(status))
		isHidden := false
		pane := member.Get("tmuxPaneId")
		for _, id := range hidden {
			// JSON object and array values have separate JS reference identities.
			if primitiveEqual(pane, id) {
				isHidden = true
				break
			}
		}
		row.Set("isHidden", raw(isHidden))
		if backend := member.String("backendType"); backend == "tmux" || backend == "iterm2" {
			row.Set("backendType", member.Get("backendType"))
		}
		result = append(result, row)
	}
	return result, nil
}

func primitiveEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	if a[0] == '{' || a[0] == '[' || b[0] == '{' || b[0] == '[' {
		return false
	}
	return bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b))
}

// RemoveMemberByPaneID performs the original first-match splice, including
// only the first matching hidden pane. Shutdown and in-process paths use their
// existing distinct member-removal consumers.
func (c *Config) RemoveMemberByPaneID(team string, pane json.RawMessage) (bool, error) {
	changed := false
	_, err := c.Mutate(team, func(file protocol.JSONObject) (protocol.JSONObject, error) {
		var members []protocol.JSONObject
		if err := json.Unmarshal(file.Get("members"), &members); err != nil {
			return file, err
		}
		for i, member := range members {
			if primitiveEqual(member.Get("tmuxPaneId"), pane) {
				members = append(members[:i:i], members[i+1:]...)
				file.Set("members", raw(members))
				changed = true
				if value := file.Get("hiddenPaneIds"); truthy(value) {
					var hidden []json.RawMessage
					if err := json.Unmarshal(value, &hidden); err != nil {
						return file, err
					}
					for j, id := range hidden {
						if primitiveEqual(id, pane) {
							hidden = append(hidden[:j:j], hidden[j+1:]...)
							file.Set("hiddenPaneIds", raw(hidden))
							break
						}
					}
				}
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

type MemberModeUpdate struct{ MemberName, Mode string }

func (c *Config) SetMemberMode(team, name, mode string) (bool, error) {
	found := false
	_, err := c.Mutate(team, func(file protocol.JSONObject) (protocol.JSONObject, error) {
		var members []protocol.JSONObject
		if err := json.Unmarshal(file.Get("members"), &members); err != nil {
			return file, err
		}
		for _, member := range members {
			if member.String("name") == name {
				found = true
				if primitiveEqual(member.Get("mode"), raw(mode)) {
					return file, nil
				}
				break
			}
		}
		if found {
			for i, member := range members {
				if member.String("name") == name {
					members[i] = member.Clone()
					members[i].Set("mode", raw(mode))
				}
			}
			file.Set("members", raw(members))
		}
		return file, nil
	})
	var missing *TeamFileNotFoundError
	if errors.As(err, &missing) {
		return false, nil
	}
	return found, err
}

func (c *Config) SetMultipleMemberModes(team string, updates []MemberModeUpdate) (bool, error) {
	_, err := c.Mutate(team, func(file protocol.JSONObject) (protocol.JSONObject, error) {
		var members []protocol.JSONObject
		if err := json.Unmarshal(file.Get("members"), &members); err != nil {
			return file, err
		}
		modes := map[string]string{}
		for _, update := range updates {
			modes[update.MemberName] = update.Mode
		}
		changed := false
		for i, member := range members {
			if mode, ok := modes[member.String("name")]; ok && !primitiveEqual(member.Get("mode"), raw(mode)) {
				members[i] = member.Clone()
				members[i].Set("mode", raw(mode))
				changed = true
			}
		}
		if changed {
			file.Set("members", raw(members))
		}
		return file, nil
	})
	var missing *TeamFileNotFoundError
	if errors.As(err, &missing) {
		return false, nil
	}
	return err == nil, err
}
