package teamstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

type LifecycleOptions struct {
	Config *Config
	Tasks  *taskstore.Service
	// Pane and worktree teardown reuse the actual native process/Git services.
	RuntimeResources func(context.Context, string, []protocol.JSONObject, bool) error
}
type Lifecycle struct{ options LifecycleOptions }

func NewLifecycle(o LifecycleOptions) (*Lifecycle, error) {
	if o.Config == nil || o.Tasks == nil || o.RuntimeResources == nil {
		return nil, errors.New("Team cleanup requires actual config, task lifecycle and native resource teardown")
	}
	return &Lifecycle{options: o}, nil
}
func identity(v protocol.JSONObject) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "teamName", Value: v.Get("name")}, protocol.JSONField{Name: "createdAt", Value: v.Get("createdAt")}, protocol.JSONField{Name: "leadSessionId", Value: v.Get("leadSessionId")})
}
func sameIdentity(a, b protocol.JSONObject) bool {
	for _, key := range []string{"teamName", "createdAt", "leadSessionId"} {
		left, right := a.Get(key), b.Get(key)
		if key == "createdAt" {
			var x, y float64
			if json.Unmarshal(left, &x) == nil && json.Unmarshal(right, &y) == nil {
				if x != y {
					return false
				}
				continue
			}
		}
		if string(protocol.StringifyValidatedJavaScriptJSON(left)) != string(protocol.StringifyValidatedJavaScriptJSON(right)) {
			return false
		}
	}
	return true
}
func (s *Lifecycle) Cleanup(ctx context.Context, name string, expected *taskstore.LifecycleToken, killPanes bool) (receipt protocol.JSONObject, err error) {
	list := SanitizeName(name)
	team := s.options.Config.Read(name)
	fallback := protocol.NewJSONObject(protocol.JSONField{Name: "teamName", Value: raw(name)}, protocol.JSONField{Name: "createdAt", Value: raw(0)})
	if team != nil {
		fallback = identity(*team)
		if !truthy(team.Get("leadSessionId")) {
			fallback.Delete("leadSessionId")
		}
	}
	observed, err := s.options.Tasks.ReadLifecycle(list)
	if err != nil {
		return receipt, err
	}
	if expected == nil && team == nil && observed.Get("activeIdentity") != nil {
		return receipt, fmt.Errorf("Cannot infer which incarnation owns missing Team config %s", name)
	}
	lifecycle := taskstore.LifecycleToken{Identity: fallback}
	_ = json.Unmarshal(observed.Get("generation"), &lifecycle.Generation)
	if value := observed.Get("activeIdentity"); value != nil {
		lifecycle.Identity = object(value)
	}
	if expected != nil {
		lifecycle = *expected
		lifecycle.Identity = expected.Identity.Clone()
	}
	if team != nil && !sameIdentity(lifecycle.Identity, identity(*team)) {
		return receipt, fmt.Errorf("Refusing cleanup after Team %s changed incarnation", name)
	}
	err = s.options.Tasks.WithTeamLifecycleTransaction(list, func(scoped *taskstore.Service) error {
		current, err := scoped.ReadLifecycle(list)
		if err != nil {
			return err
		}
		var generation float64
		_ = json.Unmarshal(current.Get("generation"), &generation)
		if generation != lifecycle.Generation || current.Get("activeIdentity") != nil && !sameIdentity(object(current.Get("activeIdentity")), lifecycle.Identity) {
			return fmt.Errorf("Refusing stale cleanup for Team %s: task-list generation changed", name)
		}
		latest := s.options.Config.Read(name)
		if latest != nil && !sameIdentity(identity(*latest), lifecycle.Identity) {
			return fmt.Errorf("Refusing stale cleanup for a newer Team %s", name)
		}
		if string(current.Get("deleted")) == "true" {
			var terminals []protocol.JSONObject
			_ = json.Unmarshal(current.Get("terminals"), &terminals)
			for _, terminal := range terminals {
				var g float64
				_ = json.Unmarshal(terminal.Get("generation"), &g)
				if g == lifecycle.Generation && sameIdentity(object(terminal.Get("identity")), lifecycle.Identity) {
					receipt = terminal.Clone()
					break
				}
			}
			if !receipt.IsObject() {
				return fmt.Errorf("Team %s was deleted without a terminal task frame", name)
			}
		} else {
			snapshot, err := scoped.Snapshot(list)
			if err != nil {
				return err
			}
			receipt, err = scoped.CompleteLifecycle(list, snapshot, lifecycle)
			if err != nil {
				return err
			}
		}
		var members []protocol.JSONObject
		if latest != nil {
			_ = json.Unmarshal(latest.Get("members"), &members)
		} else if team != nil {
			_ = json.Unmarshal(team.Get("members"), &members)
		}
		if err = s.options.RuntimeResources(ctx, name, members, killPanes); err != nil {
			return err
		}
		if err = scoped.RemoveTeamDirectory(list); err != nil {
			return fmt.Errorf("Failed to clean up Team task directory %s: %w", scoped.Directory(list), err)
		}
		if err = os.RemoveAll(s.options.Config.Directory(name)); err != nil {
			return fmt.Errorf("Failed to clean up Team directory %s: %w", s.options.Config.Directory(name), err)
		}
		return nil
	})
	return receipt, err
}
