package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamreader"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

type teamRuntimeState struct {
	runtime   *teamworker.Runtime
	plans     *teamplan.Service
	stopping  map[string]chan struct{}
	observers map[string]map[uint64]func(protocol.JSONObject)
	sequence  uint64
	watcher   *teamreader.Watcher
}

// The original TeamWatcher is a Server-owned background producer. All Team
// files, including writes made by CLI teammates, reach the leader's socket.
func (s *Server) BindTeamWatcher(reader *teamreader.Service) error {
	watcher, err := teamreader.NewWatcher(reader, func(message protocol.JSONObject, lead string) {
		if lead != "" {
			s.broadcast(lead, teamWorkerJSON(message))
			return
		}
		s.mu.Lock()
		ids := []string{}
		for id, sockets := range s.connections {
			if len(sockets) > 0 {
				ids = append(ids, id)
			}
		}
		s.mu.Unlock()
		for _, id := range ids {
			s.broadcast(id, teamWorkerJSON(message))
		}
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	state := s.teamRuntimeStateLocked()
	if s.closed || state.watcher != nil {
		s.mu.Unlock()
		_ = watcher.Close(context.Background())
		return errors.New("Team watcher already bound or server closed")
	}
	state.watcher = watcher
	s.mu.Unlock()
	watcher.Start(s.ctx)
	return nil
}

func (s *Server) teamRuntimeStateLocked() *teamRuntimeState {
	if s.teams == nil {
		s.teams = &teamRuntimeState{stopping: map[string]chan struct{}{}, observers: map[string]map[uint64]func(protocol.JSONObject){}}
	}
	return s.teams
}
func (s *Server) BindTeamRuntime(runtime *teamworker.Runtime) error {
	if runtime == nil {
		return errors.New("Actual native Team runtime is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.teamRuntimeStateLocked()
	if s.closed || state.runtime != nil {
		return errors.New("Team runtime is already bound or server closed")
	}
	state.runtime = runtime
	return nil
}
func (s *Server) TeamRuntimeRunning(planID string) bool {
	s.mu.Lock()
	var runtime *teamworker.Runtime
	if s.teams != nil {
		runtime = s.teams.runtime
	}
	s.mu.Unlock()
	return runtime != nil && runtime.IsRunning(planID)
}
func (s *Server) observeTeamSDK(id string, data json.RawMessage) {
	var message protocol.JSONObject
	if message.UnmarshalJSON(data) != nil {
		return
	}
	s.mu.Lock()
	callbacks := []func(protocol.JSONObject){}
	if s.teams != nil && !s.closed {
		for _, callback := range s.teams.observers[id] {
			callbacks = append(callbacks, callback)
		}
	}
	s.mu.Unlock()
	for _, callback := range callbacks {
		callback(message.Clone())
	}
}
func (s *Server) observeTeamOutput(id string, callback func(protocol.JSONObject)) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.runtimes[id] == nil {
		return nil, errors.New("CLI session is not running")
	}
	state := s.teamRuntimeStateLocked()
	state.sequence++
	key := state.sequence
	if state.observers[id] == nil {
		state.observers[id] = map[uint64]func(protocol.JSONObject){}
	}
	state.observers[id][key] = callback
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.teams != nil {
			delete(s.teams.observers[id], key)
			if len(s.teams.observers[id]) == 0 {
				delete(s.teams.observers, id)
			}
		}
	}, nil
}
func (s *Server) NativeSessionConnected(id string) bool {
	s.mu.Lock()
	runtime := s.runtimes[id]
	closed := s.closed
	s.mu.Unlock()
	if closed || runtime == nil {
		return false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.live != nil && !runtime.retired && runtime.ctx.Err() == nil
}
func (s *Server) WaitTeamWorkersStopped(ctx context.Context, parent string) error {
	s.mu.Lock()
	var done chan struct{}
	if s.teams != nil {
		done = s.teams.stopping[parent]
	}
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *Server) stopTeamPlanParent(ctx context.Context, parent string) error {
	s.mu.Lock()
	state := s.teamRuntimeStateLocked()
	runtime := state.runtime
	if runtime == nil {
		s.mu.Unlock()
		return nil
	}
	if done := state.stopping[parent]; done != nil {
		s.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	done := make(chan struct{})
	state.stopping[parent] = done
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if state.stopping[parent] == done {
			delete(state.stopping, parent)
			close(done)
		}
		s.mu.Unlock()
	}()
	return runtime.StopParent(ctx, parent)
}
func (s *Server) closeTeamRuntime(ctx context.Context) error {
	s.mu.Lock()
	var runtime *teamworker.Runtime
	var plans *teamplan.Service
	var watcher *teamreader.Watcher
	if s.teams != nil {
		runtime = s.teams.runtime
		plans = s.teams.plans
		watcher = s.teams.watcher
	}
	s.mu.Unlock()
	var err error
	if watcher != nil {
		err = watcher.Close(ctx)
	}
	if plans != nil {
		err = errors.Join(err, plans.Close(ctx))
	}
	if runtime != nil {
		err = errors.Join(err, runtime.Close(ctx))
	}
	return err
}
func (s *Server) teamPermissionMode(ctx context.Context, id string) (string, error) {
	s.mu.Lock()
	runtime := s.runtimes[id]
	s.mu.Unlock()
	if runtime == nil {
		return "default", nil
	}
	runtime.mu.Lock()
	live := runtime.live
	mode := runtime.options.PermissionMode
	runtime.mu.Unlock()
	if source, ok := live.(interface {
		SessionPermissionMode(context.Context) (string, error)
	}); ok {
		return source.SessionPermissionMode(ctx)
	}
	if live != nil {
		return "", unsupported("actual Team leader permission mode")
	}
	if mode == "" {
		mode = "default"
	}
	return mode, nil
}
func (s *Server) applyTeamRuntimeSnapshot(ctx context.Context, parent, team string, createdAt json.RawMessage) error {
	s.mu.Lock()
	runtime := s.runtimes[parent]
	s.mu.Unlock()
	if runtime == nil {
		return errors.New("CLI session is not running")
	}
	runtime.mu.Lock()
	live := runtime.live
	runtime.mu.Unlock()
	if source, ok := live.(interface {
		SetTeamRuntimeSnapshot(context.Context, string, json.RawMessage) error
	}); ok {
		return source.SetTeamRuntimeSnapshot(ctx, team, createdAt)
	}
	return unsupported("actual native Team runtime snapshot control")
}
func (s *Server) sendTeamLeader(ctx context.Context, id, text string) (bool, error) {
	if !s.NativeSessionConnected(id) {
		return false, nil
	}
	s.mu.Lock()
	runtime := s.runtimes[id]
	s.mu.Unlock()
	if runtime == nil {
		return false, nil
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: teamWorkerJSON("user_message")}, protocol.JSONField{Name: "content", Value: teamWorkerJSON(text)})
	runtime.mu.Lock()
	if runtime.retired || runtime.ctx.Err() != nil {
		runtime.mu.Unlock()
		return false, nil
	}
	runtime.admission = append(runtime.admission, admissionFrame{raw: teamWorkerJSON(input)})
	runtime.epoch++
	runtime.mu.Unlock()
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
	return true, nil
}

// BindTeamSessionCallbacks shares the existing native session factory, its SDK
// observation stream, admission queue and same pending-permission registry.
func (s *Server) BindTeamSessionCallbacks(o *teamworker.RuntimeOptions) {
	o.Connected = s.NativeSessionConnected
	o.WaitWorkersStopped = s.WaitTeamWorkersStopped
	o.Validation.PermissionMode = s.teamPermissionMode
	o.Prepare = func(ctx context.Context, w teamworker.WorkerRequest) (string, error) {
		return s.PrepareTeamWorker(ctx, TeamWorkerLaunch{ID: w.ID, WorkDir: w.WorkDir, Model: w.Model, Effort: w.Effort, PermissionMode: w.PermissionMode, ProviderID: w.ProviderID, Worker: w.Worker})
	}
	o.Release = func(ctx context.Context, id, text string, canSend func() bool) (bool, error) {
		if !s.NativeSessionConnected(id) {
			return false, nil
		}
		if err := s.ReleaseTeamWorker(ctx, id, text, canSend); err != nil {
			return false, err
		}
		return true, nil
	}
	o.StopWorker = s.StopTeamWorker
	o.Snapshot = s.applyTeamRuntimeSnapshot
	o.Observe = s.observeTeamOutput
	o.SendLeader = s.sendTeamLeader
}
