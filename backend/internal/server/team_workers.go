package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

type TeamWorkerLaunch struct {
	ID, WorkDir, Model, Effort, PermissionMode string
	ProviderID                                 *string
	Worker                                     teamworker.Start
	Resume                                     bool
}
type nativeTeamWorkerSession interface {
	AcknowledgeTeamWorkerModel(context.Context, string) (protocol.JSONObject, error)
	SubmitTeamTask(context.Context, protocol.JSONObject) error
}

// PrepareTeamWorker enters the same runtime/Factory/credential registry as an
// ordinary desktop session. A standalone worker has no public placeholder.
func (s *Server) PrepareTeamWorker(ctx context.Context, launch TeamWorkerLaunch) (string, error) {
	if !filepath.IsAbs(launch.WorkDir) || launch.Model == "" || launch.Worker.ParentSessionID == "" {
		return "", errors.New("Team working directory or approved worker routing is unavailable")
	}
	if launch.ID == "" {
		id, err := uuid()
		if err != nil {
			return "", err
		}
		launch.ID = id
	}
	s.mu.Lock()
	parent := s.runtimes[launch.Worker.ParentSessionID]
	closed := s.closed || s.migration.active
	s.mu.Unlock()
	if closed || parent == nil && !launch.Resume {
		return "", errors.New("Team leader must be connected before launching")
	}
	connected := false
	if parent != nil {
		parent.mu.Lock()
		connected = parent.live != nil && !parent.retired
		parent.mu.Unlock()
	}
	if !connected && !launch.Resume {
		return "", errors.New("Team leader must be connected before launching")
	}
	worker := launch.Worker
	worker.AgentDefinition = worker.AgentDefinition.Clone()
	if worker.Tools != nil {
		tools := append([]string{}, (*worker.Tools)...)
		worker.Tools = &tools
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "runtimeProviderId", Value: teamWorkerJSON(launch.ProviderID)}, protocol.JSONField{Name: "runtimeModelId", Value: teamWorkerJSON(launch.Model)}, protocol.JSONField{Name: "permissionMode", Value: teamWorkerJSON(launch.PermissionMode)})
	if launch.Effort != "" {
		metadata.Set("effortLevel", teamWorkerJSON(launch.Effort))
	}
	// Source asks to recover the same worker identity, but only adds --resume
	// when its canonical saved transcript actually exists. The requested
	// recovery still permits a disconnected lead; it is distinct from the
	// effective CLI launch mode used by the owning factory.
	resume := false
	if launch.Resume {
		if info, err := s.findSession(ctx, launch.ID); err == nil {
			resume = info.Path != ""
		}
		if err := context.Cause(ctx); err != nil {
			return "", err
		}
	}
	runtimeContext, cancel := context.WithCancel(s.ctx)
	runtime := &runtimeSession{ctx: runtimeContext, cancel: cancel, options: SessionOptions{ID: launch.ID, WorkDir: launch.WorkDir, PermissionMode: launch.PermissionMode, Metadata: teamWorkerJSON(metadata), TeamWorker: &worker, Resume: resume, OnCanonicalTranscriptMutation: func(path string) error { s.noteSessionMutation(path); return nil }}, wake: make(chan struct{}, 1), admitted: make(chan struct{})}
	s.mu.Lock()
	if s.closed || s.migration.active || s.runtimes[launch.ID] != nil || s.deleting[launch.ID] {
		s.mu.Unlock()
		cancel()
		return "", errors.New("Team worker session is already active or unavailable")
	}
	s.teamWorkerSequence++
	runtime.teamWorkerOrdinal = s.teamWorkerSequence
	s.runtimes[launch.ID] = runtime
	s.mu.Unlock()
	go s.admit(launch.ID, runtime)
	live, err := s.liveRuntime(launch.ID, runtime)
	if err == nil {
		worker, ok := live.(nativeTeamWorkerSession)
		if !ok {
			err = unsupported("Team worker actual model control and SDK input producer")
		} else {
			_, err = worker.AcknowledgeTeamWorkerModel(ctx, launch.Model)
		}
	}
	if err != nil {
		_ = s.stopSessionAndWait(context.WithoutCancel(ctx), launch.ID)
		return "", err
	}
	return launch.ID, nil
}

// ReleaseTeamWorker enqueues the original SDK user envelope. The existing
// admission task is its sole model consumer; returning means accepted input.
func (s *Server) ReleaseTeamWorker(ctx context.Context, id, text string, canSend func() bool) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	runtime := s.runtimes[id]
	unavailable := s.closed || s.migration.active
	s.mu.Unlock()
	if unavailable || runtime == nil {
		return errors.New("CLI session is not running")
	}
	uuid, err := uuid()
	if err != nil {
		return err
	}
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: teamWorkerJSON("user")}, protocol.JSONField{Name: "session_id", Value: teamWorkerJSON(id)}, protocol.JSONField{Name: "uuid", Value: teamWorkerJSON(uuid)}, protocol.JSONField{Name: "message", Value: teamWorkerJSON(map[string]any{"role": "user", "content": text})})
	runtime.mu.Lock()
	if runtime.retired || runtime.ctx.Err() != nil || runtime.options.TeamWorker == nil || runtime.live == nil || (canSend != nil && !canSend()) {
		runtime.mu.Unlock()
		return errors.New("Team launch cancelled")
	}
	runtime.admission = append(runtime.admission, admissionFrame{raw: teamWorkerJSON(map[string]any{"type": "team_worker_input", "input": input})})
	runtime.epoch++
	runtime.mu.Unlock()
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
	return nil
}

type teamRuntimeEntry struct {
	id      string
	runtime *runtimeSession
	ordinal uint64
}

func (s *Server) teamChildren(parentID string) []teamRuntimeEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []teamRuntimeEntry{}
	for id, runtime := range s.runtimes {
		runtime.mu.Lock()
		worker := runtime.options.TeamWorker
		ordinal := runtime.teamWorkerOrdinal
		runtime.mu.Unlock()
		if worker != nil && worker.ParentSessionID == parentID {
			result = append(result, teamRuntimeEntry{id, runtime, ordinal})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ordinal < result[j].ordinal })
	return result
}
func (s *Server) StopTeamWorkers(ctx context.Context, parentID string) error {
	return s.stopTeamWorkers(ctx, parentID, false)
}
func (s *Server) PauseTeamWorkers(ctx context.Context, parentID string) error {
	return s.stopTeamWorkers(ctx, parentID, true)
}
func (s *Server) stopTeamWorkers(ctx context.Context, parentID string, pause bool) error {
	if err := s.stopTeamPlanParent(ctx, parentID, pause); err != nil {
		return err
	}
	var failures []error
	for _, child := range s.teamChildren(parentID) {
		if err := s.stopSessionAndWait(ctx, child.id); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (s *Server) StopTeamWorker(ctx context.Context, id string) error {
	return s.stopSessionAndWait(ctx, id)
}
func (s *Server) TeamWorkerConnected(id string) bool {
	s.mu.Lock()
	runtime := s.runtimes[id]
	s.mu.Unlock()
	if runtime == nil {
		return false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.live != nil && !runtime.retired && runtime.options.TeamWorker != nil && runtime.ctx.Err() == nil
}
func teamPermissionFrame(data json.RawMessage, worker *teamworker.Start) json.RawMessage {
	var frame protocol.JSONObject
	if frame.UnmarshalJSON(data) != nil {
		return data
	}
	if frame.String("type") == "permission_request" {
		frame.Set("agentId", teamWorkerJSON(worker.Name+"@"+worker.TeamName))
		frame.Set("displayName", teamWorkerJSON(worker.Name))
		return teamWorkerJSON(frame)
	}
	return data
}
func (s *Server) emitTeamWorker(id string, data json.RawMessage) {
	var event protocol.JSONObject
	_ = event.UnmarshalJSON(data)
	if event.String("type") == "message_complete" || event.String("type") == "permission_request" || event.String("type") == "permission_resolved" {
		// Snapshot and replay must not interleave a resolved frame: otherwise
		// a completed permission can be resurrected after its resolution.
		s.teamEventMu.Lock()
		defer s.teamEventMu.Unlock()
	}
	s.broadcast(id, data)
	s.mu.Lock()
	runtime := s.runtimes[id]
	s.mu.Unlock()
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	worker := runtime.options.TeamWorker
	runtime.mu.Unlock()
	if worker == nil {
		var frame protocol.JSONObject
		_ = frame.UnmarshalJSON(data)
		if frame.String("type") == "message_complete" {
			// Source replays outstanding can_use_tool requests after projecting
			// result. React clears the completed turn's permissions at that frame;
			// independent teammates can still be waiting on the same request.
			ctx := context.WithoutCancel(runtime.ctx)
			permissions := []json.RawMessage{}
			runtime.mu.Lock()
			live, retired := runtime.live, runtime.retired || runtime.closing != nil || runtime.ctx.Err() != nil
			runtime.mu.Unlock()
			if live != nil && !retired {
				frames, err := live.Snapshot(ctx)
				if err != nil {
					s.options.OnUnhandled(httpapi.UnhandledError{Type: "permission_replay_failed", Severity: "error", Summary: "Cannot replay pending permissions after lead completion", Error: err})
				} else {
					for _, data := range frames {
						var pending protocol.JSONObject
						_ = pending.UnmarshalJSON(data)
						if pending.String("type") == "permission_request" {
							permissions = append(permissions, data)
						}
					}
				}
			}
			childPermissions, err := s.teamPermissionSnapshots(ctx, id)
			if err != nil {
				s.options.OnUnhandled(httpapi.UnhandledError{Type: "team_permission_replay_failed", Severity: "error", Summary: "Cannot replay pending teammate permissions after lead completion", Error: err})
				return
			}
			permissions = append(permissions, childPermissions...)
			for _, pending := range permissions {
				s.broadcast(id, pending)
			}
		}
		return
	}
	var frame protocol.JSONObject
	_ = frame.UnmarshalJSON(data)
	switch frame.String("type") {
	case "permission_request", "permission_resolved":
		s.broadcast(worker.ParentSessionID, teamPermissionFrame(data, worker))
	}
}
func (s *Server) teamPermissionSnapshots(ctx context.Context, parentID string) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	for _, entry := range s.teamChildren(parentID) {
		entry.runtime.mu.Lock()
		live, worker := entry.runtime.live, entry.runtime.options.TeamWorker
		generation := entry.runtime.disconnectState().generation
		retired := entry.runtime.retired || entry.runtime.closing != nil || entry.runtime.ctx.Err() != nil
		entry.runtime.mu.Unlock()
		if live == nil || retired {
			continue
		}
		frames, err := live.Snapshot(ctx)
		s.mu.Lock()
		owned := !s.closed && !s.migration.active && !s.deleting[entry.id] && s.runtimes[entry.id] == entry.runtime
		s.mu.Unlock()
		entry.runtime.mu.Lock()
		same := owned && !entry.runtime.retired && entry.runtime.closing == nil && entry.runtime.ctx.Err() == nil && entry.runtime.live == live && entry.runtime.disconnectState().generation == generation
		entry.runtime.mu.Unlock()
		if !same {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, data := range frames {
			var frame protocol.JSONObject
			_ = frame.UnmarshalJSON(data)
			if frame.String("type") == "permission_request" {
				result = append(result, teamPermissionFrame(data, worker))
			}
		}
	}
	return result, nil
}
func (s *Server) routeTeamPermission(ctx context.Context, parentID string, data json.RawMessage) (bool, error) {
	var frame protocol.JSONObject
	_ = frame.UnmarshalJSON(data)
	if frame.String("type") != "permission_response" && frame.String("type") != "ask_user_question_activity" {
		return false, nil
	}
	id := frame.String("requestId")
	for _, entry := range s.teamChildren(parentID) {
		entry.runtime.mu.Lock()
		live := entry.runtime.live
		entry.runtime.mu.Unlock()
		if live == nil {
			continue
		}
		frames, err := live.Snapshot(ctx)
		if err != nil {
			return false, err
		}
		for _, raw := range frames {
			var pending protocol.JSONObject
			_ = pending.UnmarshalJSON(raw)
			if pending.String("type") == "permission_request" && pending.String("requestId") == id {
				return true, live.Handle(ctx, data)
			}
		}
	}
	return false, nil
}
func (s *Server) teamSnapshot(ctx context.Context, id string, frames []json.RawMessage) ([]json.RawMessage, error) {
	permissions, err := s.teamPermissionSnapshots(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(permissions) == 0 {
		return frames, nil
	}
	result := []json.RawMessage{}
	for _, data := range frames {
		var frame protocol.JSONObject
		_ = frame.UnmarshalJSON(data)
		if frame.String("type") == "permission_requests_snapshot" {
			var ids []string
			_ = json.Unmarshal(frame.Get("toolRequestIds"), &ids)
			for _, pending := range permissions {
				var p protocol.JSONObject
				_ = p.UnmarshalJSON(pending)
				ids = append(ids, p.String("requestId"))
				result = append(result, pending)
			}
			frame.Set("toolRequestIds", teamWorkerJSON(ids))
			data = teamWorkerJSON(frame)
		}
		result = append(result, data)
	}
	return result, nil
}

func teamWorkerJSON(value any) json.RawMessage {
	b, _ := json.Marshal(value)
	return protocol.StringifyValidatedJavaScriptJSON(b)
}

func (s *Server) runtimeFailure(id string, socket *connection, err error) {
	if socket != nil {
		socket.failure(err.Error(), "USER_TURN_FAILED")
	} else {
		s.broadcast(id, teamWorkerJSON(map[string]any{"type": "error", "message": err.Error(), "code": "USER_TURN_FAILED"}))
	}
}
