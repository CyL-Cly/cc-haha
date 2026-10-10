package teamstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

type ManagerOptions struct {
	Config    *Config
	Tasks     *taskstore.Service
	Lifecycle *Lifecycle
	Now       func() time.Time
	WordSlug  func() (string, error)
}
type Manager struct {
	options    ManagerOptions
	mu         sync.Mutex
	registered map[string]taskstore.LifecycleToken
}
type CreationRequest struct {
	TeamName, Description, AgentType, LeaderModel, CWD, SessionID, ExistingTeam string
	HasDescription                                                              bool
}
type Created struct {
	TeamName, LeadAgentID string
	Team                  protocol.JSONObject
	Lifecycle             taskstore.LifecycleToken
}

func NewManager(o ManagerOptions) (*Manager, error) {
	if o.Config == nil || o.Tasks == nil || o.Lifecycle == nil || o.Now == nil || o.WordSlug == nil {
		return nil, errors.New("Team manager requires actual config/task lifecycle/clock/slug producers")
	}
	return &Manager{options: o, registered: map[string]taskstore.LifecycleToken{}}, nil
}
func (m *Manager) Registered(name string) *taskstore.LifecycleToken {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.registered[name]
	if !ok {
		return nil
	}
	value.Identity = value.Identity.Clone()
	return &value
}
func (m *Manager) unregister(name string, expected taskstore.LifecycleToken) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.registered[name]
	if ok && value.Generation == expected.Generation && sameIdentity(value.Identity, expected.Identity) {
		delete(m.registered, name)
	}
}

// Create reserves the original name and starts its generation inside one
// cross-process lifecycle transaction. The caller resolves its proposal first.
func (m *Manager) Create(ctx context.Context, request CreationRequest) (out Created, err error) {
	if request.ExistingTeam != "" {
		return out, fmt.Errorf("Already leading team %q. A leader can only manage one team at a time. Use TeamDelete to end the current team before creating a new one.", request.ExistingTeam)
	}
	if request.TeamName == "" || protocol.TrimJavaScriptString(request.TeamName) == "" {
		return out, errors.New("team_name is required for TeamCreate")
	}
	if request.SessionID == "" || request.CWD == "" || request.LeaderModel == "" {
		return out, errors.New("Team creation requires actual leader session, working directory and model")
	}
	candidate := request.TeamName
	for {
		if err = context.Cause(ctx); err != nil {
			return out, err
		}
		created := false
		err = m.options.Tasks.WithTeamLifecycleTransaction(SanitizeName(candidate), func(scoped *taskstore.Service) error {
			state, err := scoped.ReadLifecycle(SanitizeName(candidate))
			if err != nil {
				return err
			}
			if m.options.Config.Read(candidate) != nil || string(state.Get("deleted")) != "true" && state.Get("activeIdentity") != nil {
				return nil
			}
			typeName := request.AgentType
			if typeName == "" {
				typeName = "team-lead"
			}
			id := "team-lead@" + candidate
			now := m.options.Now().UnixMilli()
			team := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(candidate)}, protocol.JSONField{Name: "description", Value: nil}, protocol.JSONField{Name: "createdAt", Value: raw(now)}, protocol.JSONField{Name: "leadAgentId", Value: raw(id)}, protocol.JSONField{Name: "leadSessionId", Value: raw(request.SessionID)}, protocol.JSONField{Name: "members", Value: raw([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: raw(id)}, protocol.JSONField{Name: "name", Value: raw("team-lead")}, protocol.JSONField{Name: "agentType", Value: raw(typeName)}, protocol.JSONField{Name: "model", Value: raw(request.LeaderModel)}, protocol.JSONField{Name: "joinedAt", Value: raw(now)}, protocol.JSONField{Name: "tmuxPaneId", Value: raw("")}, protocol.JSONField{Name: "cwd", Value: raw(request.CWD)}, protocol.JSONField{Name: "subscriptions", Value: raw([]string{})})})})
			if request.HasDescription {
				team.Set("description", raw(request.Description))
			}
			lifecycle, err := scoped.BeginLifecycle(SanitizeName(candidate), identity(team))
			if err != nil {
				return err
			}
			if err = m.options.Config.Write(candidate, team); err != nil {
				return err
			}
			m.mu.Lock()
			m.registered[candidate] = lifecycle
			m.mu.Unlock()
			if err = scoped.Reset(SanitizeName(candidate)); err != nil {
				return err
			}
			if err = scoped.EnsureDirectory(SanitizeName(candidate)); err != nil {
				return err
			}
			out = Created{TeamName: candidate, LeadAgentID: id, Team: team, Lifecycle: lifecycle}
			created = true
			return nil
		})
		if err != nil {
			return out, err
		}
		if created {
			return out, nil
		}
		candidate, err = m.options.WordSlug()
		if err != nil {
			return out, err
		}
	}
}
func (m *Manager) Delete(ctx context.Context, name string) (protocol.JSONObject, error) {
	result := protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: raw(true)}, protocol.JSONField{Name: "message", Value: raw("No team name found, nothing to clean up")})
	if name == "" {
		return result, nil
	}
	if team := m.options.Config.Read(name); team != nil {
		members := []protocol.JSONObject{}
		_ = json.Unmarshal(team.Get("members"), &members)
		alive := []string{}
		for _, member := range members {
			if member.String("agentId") != team.String("leadAgentId") && !(member.String("backendType") == "process" && string(member.Get("terminated")) == "true") {
				alive = append(alive, member.String("name"))
			}
		}
		if len(alive) > 0 {
			return protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: raw(false)}, protocol.JSONField{Name: "message", Value: raw(fmt.Sprintf("Cannot cleanup team with %d registered teammate(s): %s. Use requestShutdown and wait for shutdown approval first.", len(alive), strings.Join(alive, ", ")))}, protocol.JSONField{Name: "team_name", Value: raw(name)}), nil
		}
	}
	receipt, err := m.options.Lifecycle.Cleanup(ctx, name, m.Registered(name), false)
	if err != nil {
		return result, err
	}
	var generation float64
	_ = json.Unmarshal(receipt.Get("generation"), &generation)
	m.unregister(name, taskstore.LifecycleToken{Generation: generation, Identity: object(receipt.Get("identity"))})
	tasks := []protocol.JSONObject{}
	_ = json.Unmarshal(receipt.Get("tasks"), &tasks)
	visible := []protocol.JSONObject{}
	for _, task := range tasks {
		if !truthy(object(task.Get("metadata")).Get("_internal")) {
			visible = append(visible, task)
		}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "success", Value: raw(true)}, protocol.JSONField{Name: "message", Value: raw(fmt.Sprintf("Cleaned up directories and worktrees for team %q", name))}, protocol.JSONField{Name: "team_name", Value: raw(name)}, protocol.JSONField{Name: "finalTasks", Value: raw(visible)}, protocol.JSONField{Name: "taskListSnapshotAt", Value: receipt.Get("capturedAt")}, protocol.JSONField{Name: "taskListSnapshotRevision", Value: receipt.Get("revision")}), nil
}
func (m *Manager) CleanupSession(ctx context.Context) error {
	m.mu.Lock()
	teams := map[string]taskstore.LifecycleToken{}
	for name, token := range m.registered {
		token.Identity = token.Identity.Clone()
		teams[name] = token
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for name, token := range teams {
		wg.Add(1)
		go func(name string, token taskstore.LifecycleToken) {
			defer wg.Done()
			if _, err := m.options.Lifecycle.Cleanup(ctx, name, &token, true); err == nil {
				m.unregister(name, token)
			}
		}(name, token)
	}
	wg.Wait()
	return nil
}
