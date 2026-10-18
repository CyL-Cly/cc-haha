package server

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// This callback is internal to the actual factory/control owner. It is not a
// client message or persisted field, and never calls Stop a second time.
func (s *Server) claimBackgroundTaskStopped(id string, r *runtimeSession, generation uint64, taskID string, alreadyGone bool) func() {
	s.mu.Lock()
	r.mu.Lock()
	d := r.disconnectState()
	owned := !s.closed && !s.migration.active && !s.deleting[id] && s.runtimes[id] == r && !r.retired && r.ctx.Err() == nil && r.closing == nil && d.generation == generation
	if !owned || taskID == "" {
		r.mu.Unlock()
		s.mu.Unlock()
		return nil
	}
	if _, exists := d.tasks[taskID]; !exists {
		if !alreadyGone || d.terminal[taskID] {
			r.mu.Unlock()
			s.mu.Unlock()
			return nil
		}
		// Source converges an evicted/unknown handle only after the actual
		// control confirms not_found, preserving a durable fallback identity.
		// It was never task_started, so it is not a reconnect active task ID.
		data := protocol.NewJSONObject(protocol.JSONField{Name: "task_id", Value: rawDisconnect(taskID)}, protocol.JSONField{Name: "tool_use_id", Value: rawDisconnect(taskID)}, protocol.JSONField{Name: "reason", Value: rawDisconnect("not_found")})
		d.tasks[taskID] = disconnectTask{data: data, unlisted: true}
		d.taskOrder = append(d.taskOrder, taskID)
	}
	// Claim this one task while both identity and generation are still held.
	// Other tasks are neither stopped nor finalized by a single-task control.
	tasks := claimDisconnectBookends(d, "stopped", taskID)
	r.mu.Unlock()
	s.mu.Unlock()
	// Retries select only previously confirmed bookends (status is empty),
	// never other running tasks in this runtime.
	return func() { s.persistDisconnectBookends(id, r, "", tasks) }
}

// The existing public control establishes intent before invoking the actual
// owner. A logical/transport failure clears only unconfirmed intent, so a later
// ordinary completed task retains its real completed outcome.
func (s *Server) setBackgroundTaskStopRequested(id string, r *runtimeSession, live LiveSession, taskID string, all, requested bool) {
	s.mu.Lock()
	r.mu.Lock()
	if !s.closed && !s.migration.active && !s.deleting[id] && s.runtimes[id] == r && !r.retired && r.ctx.Err() == nil && r.closing == nil && r.live == live {
		d := r.disconnectState()
		for key, task := range d.tasks {
			if (all || taskID != "" && key == taskID) && task.finalStatus == "" {
				task.stopRequested = requested
				d.tasks[key] = task
			}
		}
	}
	r.mu.Unlock()
	s.mu.Unlock()
}

// Caller holds runtime.mu. The actual terminal event is also proof of local
// stop when it precedes the requested control's acknowledgement. Unrequested
// terminals retain the original completed/failed SDK lifecycle unchanged.
func claimRequestedTaskTerminal(d *disconnectLifecycle, message protocol.JSONObject) (bool, []disconnectTask) {
	if message.String("type") != "system" || message.String("subtype") != "task_notification" {
		return false, nil
	}
	status := message.String("status")
	if status != "completed" && status != "failed" && status != "stopped" && status != "killed" {
		return false, nil
	}
	id := trimJS(message.String("task_id"))
	task, exists := d.tasks[id]
	if !exists || !task.stopRequested || d.terminal[id] {
		return false, nil
	}
	return true, claimDisconnectBookends(d, "stopped", id)
}
