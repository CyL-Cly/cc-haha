package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ReloadSessionComponents updates the existing runtime on its owner, rather
// than treating the definitions saved on disk as an applied conversation change.
func (server *Server) ReloadSessionComponents(ctx context.Context, sessionID string) (protocol.JSONObject, error) {
	encode := func(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
	empty := func(reason string) protocol.JSONObject {
		return protocol.NewJSONObject(
			protocol.JSONField{Name: "applied", Value: encode(false)}, protocol.JSONField{Name: "reason", Value: encode(reason)},
			protocol.JSONField{Name: "commands", Value: encode(0)}, protocol.JSONField{Name: "agents", Value: encode(0)},
			protocol.JSONField{Name: "plugins", Value: encode(0)}, protocol.JSONField{Name: "mcpServers", Value: encode(0)},
			protocol.JSONField{Name: "errors", Value: encode(0)},
		)
	}
	server.mu.Lock()
	current := server.runtimes[sessionID]
	server.mu.Unlock()
	if current == nil {
		return empty("not_running"), nil
	}
	current.mu.Lock()
	live, retired := current.live, current.retired
	current.mu.Unlock()
	if live == nil || retired {
		return empty("not_running"), nil
	}
	control, ok := live.(ComponentSession)
	var response protocol.JSONObject
	var err error
	if !ok {
		err = errors.New("active session has no component control")
	} else {
		timed, cancel := context.WithTimeout(ctx, 120*time.Second)
		response, err = control.ControlComponents(timed, "reload_plugins", "")
		cancel()
	}
	if err != nil {
		result := empty("failed")
		result.Set("error", encode(err.Error()))
		return result, nil
	}
	arrayCount := func(name string) int {
		var values []json.RawMessage
		if json.Unmarshal(response.Get(name), &values) != nil {
			return 0
		}
		return len(values)
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "applied", Value: encode(true)})
	result.Set("commands", encode(len(NormalizeSessionSlashCommands(response.Get("commands")))))
	for _, name := range []string{"agents", "plugins", "mcpServers"} {
		result.Set(name, encode(arrayCount(name)))
	}
	errorsCount := response.Get("error_count")
	var count float64
	if json.Unmarshal(errorsCount, &count) != nil || string(errorsCount) == "null" {
		errorsCount = encode(0)
	}
	result.Set("errors", errorsCount)
	return result, nil
}

// NormalizeSessionSlashCommands mirrors the UI notification boundary: a
// nonblank name/command, string description, and optional string argument hint.
func NormalizeSessionSlashCommands(raw json.RawMessage) []protocol.JSONObject {
	var values []json.RawMessage
	result := []protocol.JSONObject{}
	if json.Unmarshal(raw, &values) != nil {
		return result
	}
	encode := func(value string) json.RawMessage { data, _ := json.Marshal(value); return data }
	nonblank := func(value string) bool {
		return strings.TrimFunc(value, func(r rune) bool {
			return r == 9 || r == 10 || r == 11 || r == 12 || r == 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
		}) != ""
	}
	for _, value := range values {
		var name string
		if json.Unmarshal(value, &name) == nil && string(value) != "null" {
			if nonblank(name) {
				result = append(result, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: value}, protocol.JSONField{Name: "description", Value: encode("")}))
			}
			continue
		}
		var object protocol.JSONObject
		if object.UnmarshalJSON(value) != nil {
			continue
		}
		nameRaw := object.Get("name")
		if json.Unmarshal(nameRaw, &name) != nil || string(nameRaw) == "null" {
			nameRaw = object.Get("command")
			if json.Unmarshal(nameRaw, &name) != nil || string(nameRaw) == "null" {
				continue
			}
		}
		if !nonblank(name) {
			continue
		}
		description := ""
		descriptionRaw := object.Get("description")
		if json.Unmarshal(descriptionRaw, &description) != nil || string(descriptionRaw) == "null" {
			descriptionRaw = encode("")
		}
		item := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: nameRaw}, protocol.JSONField{Name: "description", Value: descriptionRaw})
		var hint string
		if json.Unmarshal(object.Get("argumentHint"), &hint) == nil && string(object.Get("argumentHint")) != "null" {
			item.Set("argumentHint", object.Get("argumentHint"))
		}
		result = append(result, item)
	}
	return result
}
