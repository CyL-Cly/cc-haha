package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strconv"
)

type LifecycleToken struct {
	Generation float64
	Identity   protocol.JSONObject
}

func lifecycleIdentity(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	var original protocol.JSONObject
	_ = original.UnmarshalJSON(value)
	next := protocol.NewJSONObject()
	for _, key := range []string{"teamName", "createdAt", "leadSessionId"} {
		if original.Get(key) != nil {
			next.Set(key, original.Get(key))
		}
	}
	return raw(next)
}

// ReadLifecycle shares the existing state validation and retains the original
// top-level passthrough fields required by Team cleanup/forward compatibility.
func (s *Service) ReadLifecycle(list string) (protocol.JSONObject, error) {
	var state lifecycle
	var original protocol.JSONObject
	data, err := os.ReadFile(s.lifecyclePath(list, ".state.json"))
	if errors.Is(err, os.ErrNotExist) {
		state = lifecycle{Terminals: []protocol.JSONObject{}}
	} else if err != nil {
		return protocol.JSONObject{}, err
	} else {
		state, err = parseLifecycle(data, list)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		_ = original.UnmarshalJSON(data)
	}
	terminals := []protocol.JSONObject{}
	for _, receipt := range state.Terminals {
		next := protocol.NewJSONObject()
		for _, key := range []string{"frameId", "generation", "identity", "tasks", "capturedAt", "revision"} {
			value := receipt.Get(key)
			if key == "identity" {
				value = lifecycleIdentity(value)
			}
			if key == "tasks" {
				var rows []json.RawMessage
				_ = json.Unmarshal(value, &rows)
				tasks := []protocol.JSONObject{}
				for _, row := range rows {
					if task := parseTask(row, false); task != nil {
						tasks = append(tasks, *task)
					}
				}
				value = raw(tasks)
			}
			next.Set(key, value)
		}
		terminals = append(terminals, next)
	}
	next := protocol.NewJSONObject(protocol.JSONField{Name: "schemaVersion", Value: raw(1)}, protocol.JSONField{Name: "generation", Value: raw(state.Generation)}, protocol.JSONField{Name: "deleted", Value: raw(state.Deleted)})
	if state.Identity != nil {
		next.Set("activeIdentity", lifecycleIdentity(state.Identity))
	}
	next.Set("terminals", raw(terminals))
	for _, field := range original.OwnProperties() {
		var key string
		_ = json.Unmarshal(field.NameJSON, &key)
		if next.Get(key) == nil && key != "activeIdentity" {
			_ = next.SetPropertyJSON(field.NameJSON, field.Value)
		}
	}
	return next, nil
}
func (s *Service) writeLifecycleState(list string, state protocol.JSONObject) (err error) {
	path := s.lifecyclePath(list, ".state.json")
	if err = os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	temporary := path + "." + strconv.Itoa(s.options.PID) + ".tmp"
	defer func() {
		if err != nil {
			_ = os.Remove(temporary)
		}
	}()
	data := protocol.StringifyValidatedJavaScriptJSON(raw(state))
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, data, "", "  "); err != nil {
		return err
	}
	if err = os.WriteFile(temporary, pretty.Bytes(), 0666); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
func (s *Service) BeginLifecycle(list string, identity protocol.JSONObject) (token LifecycleToken, err error) {
	err = s.WithTeamLifecycleTransaction(list, func(scoped *Service) error {
		state, err := scoped.ReadLifecycle(list)
		if err != nil {
			return err
		}
		generation, _ := jsonNumber(state.Get("generation"))
		generation++
		state.Set("schemaVersion", raw(1))
		state.Set("generation", raw(generation))
		state.Set("deleted", raw(false))
		state.Set("activeIdentity", raw(identity))
		if err = scoped.writeLifecycleState(list, state); err != nil {
			return err
		}
		token = LifecycleToken{generation, identity.Clone()}
		return nil
	})
	return token, err
}
func (s *Service) CompleteLifecycle(list string, snapshot Snapshot, expected LifecycleToken) (protocol.JSONObject, error) {
	return s.CompleteLifecycleJSON(list, protocol.NewJSONObject(protocol.JSONField{Name: "tasks", Value: raw(snapshot.Tasks)}, protocol.JSONField{Name: "capturedAt", Value: raw(snapshot.CapturedAt)}, protocol.JSONField{Name: "revision", Value: raw(snapshot.Revision)}), expected)
}

// CompleteLifecycleJSON retains Source spread/property order for an existing
// snapshot DTO, including fields from a newer compatible producer.
func (s *Service) CompleteLifecycleJSON(list string, snapshot protocol.JSONObject, expected LifecycleToken) (receipt protocol.JSONObject, err error) {
	err = s.WithTeamLifecycleTransaction(list, func(scoped *Service) error {
		state, err := scoped.ReadLifecycle(list)
		if err != nil {
			return err
		}
		generation, _ := jsonNumber(state.Get("generation"))
		if generation != expected.Generation || state.Get("activeIdentity") != nil && !identityEqual(state.Get("activeIdentity"), raw(expected.Identity)) {
			return fmt.Errorf("Refusing stale cleanup for task list %s: lifecycle identity changed", list)
		}
		var terminals []protocol.JSONObject
		_ = json.Unmarshal(state.Get("terminals"), &terminals)
		retained := []protocol.JSONObject{}
		for _, terminal := range terminals {
			g, _ := jsonNumber(terminal.Get("generation"))
			matches := g == expected.Generation && identityEqual(terminal.Get("identity"), raw(expected.Identity))
			if matches && string(state.Get("deleted")) == "true" {
				receipt = terminal.Clone()
				return nil
			}
			if !matches {
				retained = append(retained, terminal)
			}
		}
		var revision float64
		_ = json.Unmarshal(snapshot.Get("revision"), &revision)
		receipt = snapshot.Clone()
		receipt.Set("frameId", raw("lifecycle:"+strconv.FormatFloat(expected.Generation, 'f', -1, 64)+":"+strconv.FormatFloat(revision, 'f', -1, 64)+":"+snapshot.String("capturedAt")))
		receipt.Set("generation", raw(expected.Generation))
		receipt.Set("identity", raw(expected.Identity))
		retained = append(retained, receipt)
		if len(retained) > 8 {
			retained = retained[len(retained)-8:]
		}
		state.Set("schemaVersion", raw(1))
		state.Set("generation", raw(expected.Generation))
		state.Set("deleted", raw(true))
		state.Set("activeIdentity", raw(expected.Identity))
		state.Set("terminals", raw(retained))
		return scoped.writeLifecycleState(list, state)
	})
	return receipt, err
}
