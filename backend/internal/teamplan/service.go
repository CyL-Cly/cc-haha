package teamplan

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Runtime is bound to actual Go shared child sessions. Models/tool callers use
// the draft Store only; trusted desktop HTTP actions own this approval service.
type Runtime struct {
	Validate     func(context.Context, protocol.JSONObject) (protocol.JSONObject, error)
	Launch       func(context.Context, protocol.JSONObject) (protocol.JSONObject, error)
	Stop         func(context.Context, string) error
	IsRunning    func(context.Context, string) (bool, error)
	NotifyLeader func(context.Context, protocol.JSONObject, string) error
	StartLeader  func(context.Context, string) error
	Rehydrate    func(context.Context, protocol.JSONObject) (bool, error)
}
type Service struct {
	store     *Store
	runtime   Runtime
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	launches  map[string]bool
	approving map[string]int
	closed    bool
	wg        sync.WaitGroup
}

func NewService(store *Store, runtime Runtime) (*Service, error) {
	if store == nil || runtime.Validate == nil || runtime.Launch == nil || runtime.Stop == nil {
		return nil, errors.New("team plan service requires original store and actual validation/launch/stop runtime")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: store, runtime: runtime, ctx: ctx, cancel: cancel, launches: map[string]bool{}, approving: map[string]int{}}, nil
}
func (s *Service) GetForSession(ctx context.Context, id string) (*protocol.JSONObject, error) {
	plan, e := s.store.FindForSession(id)
	if e != nil || plan == nil {
		return plan, e
	}
	s.mu.Lock()
	launching := s.launches[plan.String("planId")] || s.approving[plan.String("planId")] > 0
	s.mu.Unlock()
	message := ""
	if plan.String("state") == "launching" && !launching {
		message = "Launch ownership was lost. Work may have started and will not be replayed."
	}
	if plan.String("state") == "running" && s.runtime.IsRunning != nil {
		if s.runtime.Rehydrate != nil {
			if _, e = s.runtime.Rehydrate(ctx, *plan); e != nil {
				return nil, e
			}
		}
		running, e := s.runtime.IsRunning(ctx, plan.String("planId"))
		if e != nil {
			return nil, e
		}
		if !running {
			message = "The worker runtime was interrupted. Started work will not be replayed."
		}
	}
	if message != "" {
		next, e := s.store.Mutate(plan.String("teamName"), CurrentIdentity(*plan), func(v protocol.JSONObject) (protocol.JSONObject, error) {
			v.Set("state", raw("interrupted"))
			launch := object(v.Get("launch"))
			if !launch.IsObject() {
				launch = protocol.NewJSONObject()
			}
			launch.Set("status", raw("failed"))
			launch.Set("error", raw(message))
			v.Set("launch", raw(launch))
			return v, nil
		})
		return &next, e
	}
	return plan, nil
}
func (s *Service) Update(ctx context.Context, team string, id Identity, patch protocol.JSONObject) (protocol.JSONObject, error) {
	current, e := s.store.Read(team)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if current == nil {
		return protocol.JSONObject{}, failure("Plan not found", 404)
	}
	var members, edits, tasks, taskEdits []protocol.JSONObject
	_ = json.Unmarshal(current.Get("members"), &members)
	_ = json.Unmarshal(patch.Get("members"), &edits)
	_ = json.Unmarshal(current.Get("tasks"), &tasks)
	_ = json.Unmarshal(patch.Get("tasks"), &taskEdits)
	find := func(rows []protocol.JSONObject, id string) *protocol.JSONObject {
		for i := range rows {
			if rows[i].String("id") == id {
				return &rows[i]
			}
		}
		return nil
	}
	for _, edit := range edits {
		if find(members, edit.String("id")) == nil {
			return protocol.JSONObject{}, failure("Unknown member", 400)
		}
	}
	for _, edit := range taskEdits {
		if find(tasks, edit.String("id")) == nil {
			return protocol.JSONObject{}, failure("Unknown task", 400)
		}
	}
	catalog := object(current.Get("agentCatalog"))
	for i, member := range members {
		edit := find(edits, member.String("id"))
		if edit == nil {
			continue
		}
		agentType := member.String("agentType")
		if edit.Get("agentType") != nil {
			agentType = edit.String("agentType")
		}
		snapshot := object(catalog.Get(agentType))
		if !snapshot.IsObject() {
			return protocol.JSONObject{}, failure("Agent preset is unavailable", 400)
		}
		changed := agentType != member.String("agentType")
		model := snapshot.String("model")
		if model == "" || model == "inherit" {
			model = object(current.Get("leaderRuntime")).String("modelId")
		}
		suggested := protocol.NewJSONObject(protocol.JSONField{Name: "providerId", Value: object(member.Get("runtime")).Get("providerId")}, protocol.JSONField{Name: "modelId", Value: raw(model)})
		if effort := snapshot.String("effortLevel"); effort != "" {
			suggested.Set("effortLevel", raw(effort))
		}
		next := member.Clone()
		next.Set("agentType", raw(agentType))
		next.Set("agentSnapshot", raw(snapshot.Clone()))
		if changed {
			next.Set("suggestedRuntime", raw(suggested))
			if edit.Get("runtime") == nil && member.String("runtimeSource") != "human" {
				next.Set("runtime", raw(suggested))
			}
		}
		if edit.Get("runtime") != nil {
			next.Set("runtime", edit.Get("runtime"))
			next.Set("runtimeSource", raw("human"))
		}
		members[i] = next
	}
	for i, task := range tasks {
		if edit := find(taskEdits, task.String("id")); edit != nil {
			next := task.Clone()
			next.Assign(*edit)
			tasks[i] = next
		}
	}
	return s.store.Replace(team, id, protocol.NewJSONObject(protocol.JSONField{Name: "members", Value: raw(members)}, protocol.JSONField{Name: "tasks", Value: raw(tasks)}), true)
}
func (s *Service) start(plan protocol.JSONObject) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return context.Canceled
	}
	s.launches[plan.String("planId")] = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); delete(s.launches, plan.String("planId")); s.mu.Unlock() }()
		result, e := s.runtime.Launch(s.ctx, plan.Clone())
		if e == nil {
			current, readError := s.store.Read(plan.String("teamName"))
			if readError != nil {
				e = readError
			} else if current != nil && current.String("planId") == plan.String("planId") && current.String("state") == "launching" {
				_, e = s.store.Mutate(plan.String("teamName"), CurrentIdentity(*current), func(v protocol.JSONObject) (protocol.JSONObject, error) {
					v.Set("state", raw("running"))
					launch := object(v.Get("launch"))
					if !launch.IsObject() {
						launch = protocol.NewJSONObject()
					}
					launch.Set("status", raw("running"))
					launch.Set("memberIds", result.Get("memberIds"))
					v.Set("launch", raw(launch))
					return v, nil
				})
			}
		}
		if e != nil {
			_ = s.runtime.Stop(s.ctx, plan.String("planId"))
			current, readError := s.store.Read(plan.String("teamName"))
			if readError != nil || current == nil || current.String("planId") != plan.String("planId") || current.String("state") != "launching" {
				return
			}
			_, _ = s.store.Mutate(plan.String("teamName"), CurrentIdentity(*current), func(v protocol.JSONObject) (protocol.JSONObject, error) {
				state := "launch_failed"
				var interrupted interface{ ExecutionStarted() bool }
				if errors.As(e, &interrupted) && interrupted.ExecutionStarted() {
					state = "interrupted"
				}
				v.Set("state", raw(state))
				launch := object(v.Get("launch"))
				if !launch.IsObject() {
					launch = protocol.NewJSONObject()
				}
				launch.Set("status", raw("failed"))
				launch.Set("error", raw(e.Error()))
				v.Set("launch", raw(launch))
				return v, nil
			})
		}
	}()
	return nil
}
func (s *Service) Approve(ctx context.Context, team string, action protocol.JSONObject) (protocol.JSONObject, error) {
	current, e := s.store.Read(team)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if current == nil {
		return protocol.JSONObject{}, failure("Plan not found", 404)
	}
	validated := current.Clone()
	id := IdentityFrom(action)
	request := action.String("requestId")
	if object(current.Get("approvedSnapshot")).String("requestId") != request {
		if current.String("planId") != id.PlanID || current.String("sessionId") != id.SessionID || current.String("incarnationId") != id.IncarnationID || numeric(current.Get("revision")) != id.ExpectedRevision {
			return protocol.JSONObject{}, failure("Plan changed; refresh before continuing", 409)
		}
		validated, e = s.runtime.Validate(ctx, current.Clone())
		if e != nil {
			return protocol.JSONObject{}, failure(e.Error(), 400)
		}
		if current.String("state") == "review_pending" && s.runtime.StartLeader != nil {
			if e = s.runtime.StartLeader(ctx, current.String("sessionId")); e != nil {
				return protocol.JSONObject{}, failure("Team leader could not start: "+e.Error(), 409)
			}
		}
	}
	s.mu.Lock()
	s.approving[current.String("planId")]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.approving[current.String("planId")]--
		if s.approving[current.String("planId")] == 0 {
			delete(s.approving, current.String("planId"))
		}
		s.mu.Unlock()
	}()
	plan, committed, e := s.store.Approve(team, id, request, validated)
	if e != nil {
		return plan, e
	}
	if committed {
		e = s.start(plan)
	}
	return plan, e
}
func (s *Service) Action(ctx context.Context, team, kind string, action protocol.JSONObject) (protocol.JSONObject, error) {
	id := IdentityFrom(action)
	if kind == "retry" {
		return s.store.Mutate(team, id, func(v protocol.JSONObject) (protocol.JSONObject, error) {
			if v.String("state") != "launch_failed" {
				return v, failure("Only failed launches can retry", 409)
			}
			v.Set("state", raw("review_pending"))
			v.Delete("approvedSnapshot")
			v.Delete("launch")
			return v, nil
		})
	}
	plan, e := s.store.Mutate(team, id, func(v protocol.JSONObject) (protocol.JSONObject, error) {
		if kind == "return" && v.String("state") != "draft" && v.String("state") != "review_pending" {
			return v, failure("Only a pending plan can return to planning", 409)
		}
		if kind == "cancel" && v.String("state") == "running" {
			return v, failure("Use the running team stop control", 409)
		}
		state := "draft"
		if kind == "cancel" {
			state = "cancelled"
		}
		v.Set("state", raw(state))
		v.Set("feedback", action.Get("feedback"))
		return v, nil
	})
	if e != nil {
		return plan, e
	}
	if kind == "cancel" {
		if e = s.runtime.Stop(ctx, plan.String("planId")); e != nil {
			return plan, e
		}
	}
	if s.runtime.NotifyLeader != nil {
		notification := "returned"
		if kind == "cancel" {
			notification = "cancelled"
		}
		if e = s.runtime.NotifyLeader(ctx, plan.Clone(), notification); e != nil {
			return s.store.Mutate(team, CurrentIdentity(plan), func(v protocol.JSONObject) (protocol.JSONObject, error) {
				v.Set("controlDeliveryError", raw(e.Error()))
				return v, nil
			})
		}
	}
	return plan, nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	ids := []string{}
	for id := range s.launches {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	var err error
	for _, id := range ids {
		err = errors.Join(err, s.runtime.Stop(ctx, id))
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return errors.Join(err, context.Cause(ctx))
	}
}
