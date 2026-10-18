package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RuntimeTimer represents an owned timeout, including already queued callbacks.
// The callback also verifies its identity; Stop alone cannot revoke a callback
// which has already been made runnable by the timer implementation.
type RuntimeTimer interface{ Stop() bool }

const disconnectedWorkCeiling = 31 * time.Minute

type disconnectTask struct {
	data           protocol.JSONObject
	agent          bool
	finalStatus    string
	bookendPending bool
	stopRequested  bool
	unlisted       bool
}
type disconnectLifecycle struct {
	generation, sequence                         uint64
	timer                                        RuntimeTimer
	kind                                         string
	startupExpired                               bool
	cliRunning, rootWaiting, rootActive, seenSDK bool
	tasks                                        map[string]disconnectTask
	taskOrder                                    []string
	terminal                                     map[string]bool
	tool, computer                               map[string]bool
	persisted                                    map[string]bool
	persistMu                                    sync.Mutex
	finalizationScheduled                        bool
	finalizationSequence                         uint64
	finalizationRetries                          int
	afterClose                                   func(error)
	closingNotifications                         []protocol.JSONObject
}

func (r *runtimeSession) disconnectState() *disconnectLifecycle {
	if r.disconnect == nil {
		r.disconnect = &disconnectLifecycle{tasks: map[string]disconnectTask{}, terminal: map[string]bool{}, tool: map[string]bool{}, computer: map[string]bool{}, persisted: map[string]bool{}}
	}
	return r.disconnect
}
func (d *disconnectLifecycle) cancelTimer() {
	d.sequence++
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.kind = ""
	d.startupExpired = false
}
func (s *Server) afterRuntimeTimer(delay time.Duration, f func()) RuntimeTimer {
	if s.options.AfterRuntimeTimer != nil {
		return s.options.AfterRuntimeTimer(delay, f)
	}
	return time.AfterFunc(delay, f)
}
func (s *Server) cancelDisconnect(r *runtimeSession) {
	r.mu.Lock()
	r.disconnectState().cancelTimer()
	r.mu.Unlock()
}
func (s *Server) beginDisconnectGeneration(r *runtimeSession) uint64 {
	r.mu.Lock()
	d := r.disconnectState()
	d.generation++
	d.cliRunning, d.rootWaiting, d.rootActive, d.seenSDK = false, false, false, false
	clear(d.tool)
	clear(d.computer)
	generation := d.generation
	r.mu.Unlock()
	return generation
}
func (s *Server) ownsDisconnectGeneration(id string, r *runtimeSession, generation uint64) bool {
	s.mu.Lock()
	owned := !s.closed && !s.migration.active && !s.deleting[id] && s.runtimes[id] == r
	s.mu.Unlock()
	r.mu.Lock()
	owned = owned && !r.retired && r.ctx.Err() == nil && r.closing == nil && r.disconnectState().generation == generation
	r.mu.Unlock()
	return owned
}

func disconnectRootActivity(m protocol.JSONObject) bool {
	if m.String("type") == "system" {
		return m.String("subtype") == "init"
	}
	return (m.String("type") == "assistant" || m.String("type") == "stream_event") && m.String("parent_tool_use_id") == ""
}
func (d *disconnectLifecycle) observeSDK(m protocol.JSONObject) bool {
	d.seenSDK = true
	if disconnectRootActivity(m) {
		d.rootWaiting = false
		d.rootActive = true
	}
	if m.String("type") == "result" {
		d.rootWaiting = d.cliRunning
		d.rootActive = false
	}
	if m.String("type") != "system" {
		return true
	}
	subtype := m.String("subtype")
	if subtype == "session_state_changed" {
		d.cliRunning = m.String("state") == "running"
		d.rootWaiting = false
		d.rootActive = d.cliRunning
	}
	if subtype != "task_started" && subtype != "task_progress" && subtype != "task_notification" {
		return true
	}
	id := trimJS(m.String("task_id"))
	if id == "" {
		return true
	}
	if d.terminal[id] || d.tasks[id].finalStatus != "" {
		return false
	}
	if subtype == "task_progress" {
		return true
	}
	if subtype == "task_notification" {
		status := m.String("status")
		if status == "completed" || status == "failed" || status == "stopped" || status == "killed" {
			d.removeTask(id)
			// The normal terminal producer is also authoritative. A later progress
			// notification must never recreate a running task in the UI.
			d.terminal[id] = true
		}
		if status != "running" {
			return true
		}
	}
	old, exists := d.tasks[id]
	if !exists {
		d.taskOrder = append(d.taskOrder, id)
	}
	data := old.data
	if !data.IsObject() {
		data = protocol.NewJSONObject()
	}
	for _, name := range []string{"task_id", "task_type", "tool_use_id", "owner_agent_id", "workflow_run_id", "description", "output_file"} {
		if value := m.Get(name); value != nil {
			data.Set(name, value)
		}
	}
	data.Set("task_id", rawDisconnect(id))
	kind := data.String("task_type")
	agent := old.agent || kind == "local_agent" || kind == "remote_agent"
	d.tasks[id] = disconnectTask{data: data, agent: agent, stopRequested: old.stopRequested}
	return true
}
func (task disconnectTask) active() bool {
	return task.finalStatus == "" || task.agent && task.bookendPending
}
func (d *disconnectLifecycle) hasActiveTasks() bool {
	for _, task := range d.tasks {
		if task.active() {
			return true
		}
	}
	return false
}
func (d *disconnectLifecycle) removeTask(id string) {
	delete(d.tasks, id)
	for i, current := range d.taskOrder {
		if current == id {
			copy(d.taskOrder[i:], d.taskOrder[i+1:])
			d.taskOrder = d.taskOrder[:len(d.taskOrder)-1]
			return
		}
	}
}

func (s *Server) disconnectSnapshot(r *runtimeSession, frames []json.RawMessage) []json.RawMessage {
	r.mu.Lock()
	d := r.disconnectState()
	ids := []string{}
	for _, id := range d.taskOrder {
		if !d.tasks[id].unlisted {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	result := make([]json.RawMessage, len(frames))
	for i, data := range frames {
		var frame protocol.JSONObject
		if frame.UnmarshalJSON(data) == nil && frame.String("type") == "session_state" {
			if frame.Get("activeBackgroundTaskIds") == nil {
				frame.Set("activeBackgroundTaskIds", rawDisconnect(ids))
			}
			result[i], _ = frame.MarshalJSON()
		} else {
			result[i] = data
		}
	}
	return result
}
func rawDisconnect(value any) json.RawMessage { b, _ := json.Marshal(value); return b }

// These predicates are the frozen MAIN handler's active-work and background-
// only boundaries. Transport admission may remain blocked draining Bash after
// the root result; rootWaiting is what distinguishes that from a user turn.
func (d *disconnectLifecycle) decision(pending, snapshotActive, team bool, opening bool) string {
	if len(d.tool) > 0 {
		return "permission"
	}
	if opening {
		return "idle"
	}
	activeUser := pending && !d.rootWaiting || d.rootActive
	activeTasks := d.hasActiveTasks()
	if activeTasks && !activeUser && (!d.cliRunning || d.rootWaiting) && !team && len(d.computer) == 0 {
		onlyShell := true
		for _, task := range d.tasks {
			onlyShell = onlyShell && (!task.active() || !task.agent)
		}
		if onlyShell {
			return "background"
		}
	}
	busy := pending || snapshotActive || d.cliRunning || activeTasks || team
	if busy {
		return ""
	}
	return "idle"
}

func (s *Server) observeDisconnectSDK(id string, r *runtimeSession, generation uint64, data json.RawMessage) bool {
	if !s.ownsDisconnectGeneration(id, r, generation) {
		s.captureClosingTaskNotification(id, r, generation, data)
		return false
	}
	var m protocol.JSONObject
	if m.UnmarshalJSON(data) != nil {
		return true
	}
	r.mu.Lock()
	d := r.disconnectState()
	stopped, tasks := claimRequestedTaskTerminal(d, m)
	allowed := false
	if !stopped {
		allowed = d.observeSDK(m)
	}
	r.mu.Unlock()
	if stopped {
		s.persistDisconnectBookends(id, r, "", tasks)
		s.armDisconnect(id)
		return false
	}
	if allowed && m.String("type") == "system" && m.String("subtype") == "task_notification" {
		s.persistDisconnectNotification(id, r, m)
	}
	s.armDisconnect(id)
	return allowed
}

func (s *Server) captureClosingTaskNotification(id string, r *runtimeSession, generation uint64, data json.RawMessage) {
	var message protocol.JSONObject
	if message.UnmarshalJSON(data) != nil || message.String("type") != "system" || message.String("subtype") != "task_notification" {
		return
	}
	status := message.String("status")
	if message.String("tool_use_id") == "" || status != "completed" && status != "failed" && status != "stopped" && status != "killed" {
		return
	}
	s.mu.Lock()
	owned := s.runtimes[id] == r
	s.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.disconnectState()
	if owned && d.generation == generation && (r.retired || r.closing != nil) {
		d.closingNotifications = append(d.closingNotifications, message.Clone())
	}
}
func (s *Server) flushClosingTaskNotifications(id string, r *runtimeSession, failure error) {
	if failure != nil {
		return
	}
	r.mu.Lock()
	d := r.disconnectState()
	messages := d.closingNotifications
	d.closingNotifications = nil
	r.mu.Unlock()
	for _, message := range messages {
		// These statuses came from the same native owner. Saving follows its
		// completed Close/join, and still observes deletion/migration policy.
		if s.persistDisconnectNotification(id, r, message, context.WithoutCancel(r.ctx)) == nil {
			r.mu.Lock()
			d.terminal[message.String("task_id")] = true
			d.removeTask(message.String("task_id"))
			r.mu.Unlock()
		}
	}
}

type runtimeLiveness interface{ RuntimeAlive() bool }
type runtimeTransition interface{ RuntimeTransitionPending() <-chan struct{} }

func disconnectRuntimeTransition(live LiveSession) <-chan struct{} {
	if current, ok := live.(runtimeTransition); ok {
		return current.RuntimeTransitionPending()
	}
	return nil
}

// A Go Host may already have closed without emitting CLI idle (for example a
// worker shutdown or a failed replacement). Check the actual engine boundary,
// never infer process loss from a model error result.
func (s *Server) closeLostDisconnectRuntime(id string, r *runtimeSession, live LiveSession) bool {
	state, ok := live.(runtimeLiveness)
	if !ok || state.RuntimeAlive() {
		return false
	}
	s.mu.Lock()
	r.mu.Lock()
	if s.closed || s.migration.active || s.runtimes[id] != r || r.retired || r.live != live || r.closing != nil {
		r.mu.Unlock()
		s.mu.Unlock()
		return true
	}
	d := r.disconnectState()
	d.cancelTimer()
	d.cliRunning, d.rootWaiting, d.rootActive = false, false, false
	clear(d.tool)
	clear(d.computer)
	r.live = nil
	r.slashCommands = nil
	if r.revokeCollaboration != nil {
		r.revokeCollaboration()
		r.revokeCollaboration = nil
	}
	closing := make(chan struct{})
	r.closing = closing
	s.apiOperations.Add(1)
	r.mu.Unlock()
	s.mu.Unlock()
	go func() {
		defer s.apiOperations.Done()
		err := live.Close(r.ctx)
		if err == nil {
			s.bookendDisconnectTasks(id, r, "runtime_exit")
		}
		r.mu.Lock()
		r.closeError = err
		r.closing = nil
		close(closing)
		r.mu.Unlock()
	}()
	return true
}

func (s *Server) emitDisconnectGeneration(id string, r *runtimeSession, generation uint64, data json.RawMessage) {
	if !s.ownsDisconnectGeneration(id, r, generation) {
		if s.retiringPermissionResolution(id, r, generation, data) {
			s.emitTeamWorker(id, data)
		}
		return
	}
	var m protocol.JSONObject
	if m.UnmarshalJSON(data) != nil {
		return
	}
	r.mu.Lock()
	d := r.disconnectState()
	allowed := true
	stopped := false
	var tasks []disconnectTask
	switch m.String("type") {
	case "message_complete":
		// Original result settles its registered foreground token before the
		// projected frame can prompt a client's immediate sync_state request.
		settleRegisteredUserTurn(r, r.activeUserTurn)
	case "permission_request":
		// MAIN rearms the permission grace when can_use_tool arrives. A new
		// request gets its own full automatic-question/model wait allowance.
		if d.kind == "permission" {
			d.cancelTimer()
		}
		d.tool[m.String("requestId")] = true
	case "computer_use_permission_request":
		d.computer[m.String("requestId")] = true
	case "background_task_stop_failed":
		if task, exists := d.tasks[m.String("taskId")]; exists && task.finalStatus == "" {
			task.stopRequested = false
			d.tasks[m.String("taskId")] = task
		}
	case "permission_resolved":
		delete(d.tool, m.String("requestId"))
		delete(d.computer, m.String("requestId"))
	case "system_notification":
		if m.String("subtype") == "task_started" || m.String("subtype") == "task_progress" || m.String("subtype") == "task_notification" {
			var task protocol.JSONObject
			_ = task.UnmarshalJSON(m.Get("data"))
			// A terminal producer can beat the control acknowledgement. Its
			// actual stop intent must keep the strict durable bookend, rather
			// than remove the task before a failed persistence can be retried.
			stopped, tasks = claimRequestedTaskTerminal(d, task)
			allowed = !stopped && !d.terminal[task.String("task_id")] && d.tasks[task.String("task_id")].finalStatus == ""
		}
	}
	r.mu.Unlock()
	if stopped {
		s.persistDisconnectBookends(id, r, "", tasks)
		s.armDisconnect(id)
		return
	}
	if !allowed {
		return
	}
	if m.String("type") == "system_notification" && m.String("subtype") == "task_notification" {
		var task protocol.JSONObject
		_ = task.UnmarshalJSON(m.Get("data"))
		s.persistDisconnectNotification(id, r, task)
	}
	s.emitTeamWorker(id, data)
	// A raw can_use_tool request is projected before its SDK callback. Its own
	// timeout is independent of any earlier grace or shell ceiling.
	s.armDisconnect(id)
}

func (s *Server) retiringPermissionResolution(id string, r *runtimeSession, generation uint64, data json.RawMessage) bool {
	var frame protocol.JSONObject
	if frame.UnmarshalJSON(data) != nil || frame.String("type") != "permission_resolved" {
		return false
	}
	s.mu.Lock()
	owned := !s.closed && !s.migration.active && !s.deleting[id] && s.runtimes[id] == r
	s.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.disconnectState()
	requestID := frame.String("requestId")
	if !owned || d.generation != generation || !r.retired && r.closing == nil || !d.tool[requestID] && !d.computer[requestID] {
		return false
	}
	delete(d.tool, requestID)
	delete(d.computer, requestID)
	return true
}

func (s *Server) persistDisconnectNotification(id string, r *runtimeSession, message protocol.JSONObject, contexts ...context.Context) error {
	if message.String("type") != "system" || message.String("subtype") != "task_notification" {
		return nil
	}
	status := message.String("status")
	if status == "killed" {
		status = "stopped"
	}
	tool := message.String("tool_use_id")
	if tool == "" || status != "completed" && status != "failed" && status != "stopped" {
		return nil
	}
	n := protocol.NewJSONObject()
	task := message.String("task_id")
	if task == "" {
		task = tool
	}
	n.Set("taskId", rawDisconnect(task))
	n.Set("toolUseId", rawDisconnect(tool))
	n.Set("status", rawDisconnect(status))
	for source, target := range map[string]string{"owner_agent_id": "ownerAgentId", "workflow_run_id": "workflowRunId", "summary": "summary", "result": "result", "output_file": "outputFile"} {
		if value := message.String(source); value != "" {
			n.Set(target, rawDisconnect(value))
		}
	}
	timestamp := message.String("timestamp")
	if timestamp == "" {
		timestamp = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	n.Set("timestamp", rawDisconnect(timestamp))
	// The raw UUID wins when present; otherwise normalize before assigning the
	// fallback timestamp so duplicate projection/SDK callbacks share a key.
	key := message.String("uuid")
	if key == "" {
		identity := n.Clone()
		if message.String("timestamp") == "" {
			identity.Delete("timestamp")
		}
		keyBytes, _ := identity.MarshalJSON()
		key = string(keyBytes)
	}
	r.mu.Lock()
	d := r.disconnectState()
	r.mu.Unlock()
	d.persistMu.Lock()
	defer d.persistMu.Unlock()
	if d.persisted[key] {
		return nil
	}
	ctx := r.ctx
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	if err := s.appendDisconnectNotification(ctx, id, n); err != nil {
		s.options.OnUnhandled(httpapi.UnhandledError{Type: "task_notification_persistence_failed", Severity: "error", Summary: "Cannot save terminal task notification for " + id, Error: err})
		return err
	}
	d.persisted[key] = true
	return nil
}
func (s *Server) appendDisconnectNotification(ctx context.Context, id string, n protocol.JSONObject) error {
	persist, err := s.shouldPersist(ctx)
	if err != nil || !persist {
		return err
	}
	info, err := s.findSession(ctx, id)
	var api *apiError
	if errors.As(err, &api) && api.status == 404 {
		return nil
	}
	if err != nil || info.memory {
		return err
	}
	s.mu.Lock()
	deleting := s.deleting[id] || s.migration.active
	s.mu.Unlock()
	if deleting || ctx.Err() != nil {
		return nil
	}
	file, err := os.OpenFile(info.Path, os.O_WRONLY|os.O_APPEND, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	persist, err = s.shouldPersist(ctx)
	if err != nil || !persist || ctx.Err() != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"type": "cc-haha-task-notification", "isMeta": true, "taskNotification": n, "timestamp": n.String("timestamp")})
	if err != nil {
		return err
	}
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		s.noteSessionMutation(info.Path)
	}
	return err
}

func (s *Server) bookendDisconnectTasks(id string, r *runtimeSession, status string) {
	r.mu.Lock()
	tasks := claimDisconnectBookends(r.disconnectState(), status, "")
	r.mu.Unlock()
	s.persistDisconnectBookends(id, r, status, tasks)
}

// Caller holds runtime.mu. Confirmed Stop/Close is the local-stop authority.
// Agent persistence counts as activity only while this exact bookend is in
// flight; retry timers and stopped shell persistence are not running tasks.
func claimDisconnectBookends(d *disconnectLifecycle, status, onlyTask string) []disconnectTask {
	tasks := make([]disconnectTask, 0, len(d.tasks))
	for _, key := range d.taskOrder {
		task, exists := d.tasks[key]
		if !exists || onlyTask != "" && key != onlyTask || d.terminal[key] || task.bookendPending || task.finalStatus == "" && status == "" {
			continue
		}
		if task.finalStatus == "" {
			task.finalStatus = status
			if status == "runtime_exit" {
				if task.agent {
					task.finalStatus = "stopped"
				} else {
					task.finalStatus = "failed"
				}
			}
		}
		task.bookendPending = true
		d.tasks[key] = task
		task.data = task.data.Clone()
		tasks = append(tasks, task)
	}
	return tasks
}

func (s *Server) persistDisconnectBookends(id string, r *runtimeSession, status string, tasks []disconnectTask) {
	r.mu.Lock()
	d := r.disconnectState()
	r.mu.Unlock()
	failed := false
	for _, task := range tasks {
		m := task.data.Clone()
		m.Set("type", rawDisconnect("system"))
		m.Set("subtype", rawDisconnect("task_notification"))
		m.Set("status", rawDisconnect(task.finalStatus))
		description := task.data.String("description")
		if description == "" {
			description = task.data.String("task_id")
		}
		if description == "" {
			description = "Background task"
		}
		summary := description + " " + task.finalStatus
		if task.finalStatus == "failed" {
			summary = description + ": runtime connection lost; task completion could not be confirmed"
		}
		m.Set("summary", rawDisconnect(summary))
		if task.finalStatus == "failed" {
			m.Set("reason", rawDisconnect("runtime_exit"))
		}
		var failure error
		for attempt := 0; attempt < 3; attempt++ {
			failure = s.persistDisconnectNotification(id, r, m)
			if failure == nil {
				break
			}
		}
		if failure != nil {
			r.mu.Lock()
			if current, exists := d.tasks[m.String("task_id")]; exists && current.finalStatus == task.finalStatus {
				current.bookendPending = false
				current.stopRequested = false
				d.tasks[m.String("task_id")] = current
			}
			r.mu.Unlock()
			failed = true
			s.emitTeamWorker(id, rawDisconnect(map[string]any{"type": "background_task_stop_failed", "taskId": m.String("task_id"), "message": "Background task ended, but its terminal state could not be saved"}))
			continue
		}
		r.mu.Lock()
		if d.terminal[m.String("task_id")] {
			r.mu.Unlock()
			continue
		}
		d.terminal[m.String("task_id")] = true
		d.removeTask(m.String("task_id"))
		r.mu.Unlock()
		s.emitTeamWorker(id, rawDisconnect(map[string]any{"type": "system_notification", "subtype": "task_notification", "data": m}))
	}
	if failed {
		s.retryDisconnectFinalization(id, r, status)
	}
}

// Retain failed authoritative bookends for the same task, as MAIN does. The
// retry is owned and joined by Server.Close; cancellation also stops its timer.
func (s *Server) retryDisconnectFinalization(id string, r *runtimeSession, status string) {
	s.mu.Lock()
	r.mu.Lock()
	d := r.disconnectState()
	delays := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}
	if s.closed || s.migration.active || s.deleting[id] || r.retired || d.finalizationScheduled || d.finalizationRetries >= len(delays) {
		r.mu.Unlock()
		s.mu.Unlock()
		return
	}
	d.finalizationScheduled = true
	d.finalizationSequence++
	delay := delays[d.finalizationRetries]
	d.finalizationRetries++
	ticket := d.finalizationSequence
	s.apiOperations.Add(1)
	r.mu.Unlock()
	s.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			r.mu.Lock()
			if d.finalizationSequence == ticket {
				d.finalizationScheduled = false
			}
			r.mu.Unlock()
			s.apiOperations.Done()
		})
	}
	ready := make(chan struct{})
	var stopCancellation func() bool
	timer := s.afterRuntimeTimer(delay, func() {
		<-ready
		defer stopCancellation()
		defer release()
		r.mu.Lock()
		if d.finalizationSequence == ticket {
			d.finalizationScheduled = false
		}
		r.mu.Unlock()
		if s.ctx.Err() == nil {
			s.bookendDisconnectTasks(id, r, status)
		}
	})
	stopCancellation = context.AfterFunc(s.ctx, func() {
		if timer.Stop() {
			release()
		}
	})
	close(ready)
}
