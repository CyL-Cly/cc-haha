package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"golang.org/x/net/websocket"
)

// These are public HTTP/WS transport and owner-lifecycle regressions. The
// injected session owns its SDK events, real pending-request snapshot and Stop
// acknowledgement; it does not execute an Agent, model or operating-system job.
// No test writes Server.runtimes, conversation states or disconnect task maps.
type liveStatusPublicOwner struct {
	mu              sync.Mutex
	emit            Emit
	sdk             func(json.RawMessage)
	onStopped       func(string, bool)
	onClosed        func(context.Context, LiveSession) error
	permissions     map[string]json.RawMessage
	permissionOrder []string
	stopResults     map[string]string
	decoySnapshot   bool
	snapshotArmed   bool
	snapshotEntered chan struct{}
	snapshotRelease chan struct{}
	received        chan json.RawMessage
	handled         chan json.RawMessage
	finish          chan struct{}
	tail            chan struct{}
	result          chan struct{}
	closed          chan struct{}
	closeOnce       sync.Once
}

func newLiveStatusPublicOwner() *liveStatusPublicOwner {
	return &liveStatusPublicOwner{
		permissions: map[string]json.RawMessage{}, stopResults: map[string]string{},
		received: make(chan json.RawMessage, 16), handled: make(chan json.RawMessage, 16),
		closed: make(chan struct{}),
	}
}
func (o *liveStatusPublicOwner) attach(options SessionOptions, emit Emit) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.emit, o.sdk, o.onClosed = emit, options.OnSDK, options.OnRuntimeClosed
	// Reflection is restricted to the new optional typed callback. This keeps
	// the test-only preimage compilable so its initial RED is HTTP 404 rather
	// than an undefined production field; Stop cases require the actual seam.
	field := reflect.ValueOf(options).FieldByName("OnBackgroundTaskStopped")
	if field.IsValid() && field.CanInterface() {
		if claim, ok := field.Interface().(func(string, bool) func()); ok {
			o.onStopped = func(id string, gone bool) {
				if work := claim(id, gone); work != nil {
					work()
				}
			}
		}
	}
}
func (o *liveStatusPublicOwner) SDK(raw string) {
	o.mu.Lock()
	callback := o.sdk
	o.mu.Unlock()
	if callback != nil {
		callback(json.RawMessage(raw))
	}
}
func (o *liveStatusPublicOwner) output(raw json.RawMessage) {
	o.mu.Lock()
	emit := o.emit
	o.mu.Unlock()
	if emit != nil {
		emit(append(json.RawMessage(nil), raw...))
	}
}
func (o *liveStatusPublicOwner) permission(id, kind string) {
	value := map[string]any{"type": kind, "requestId": id, "toolName": "Write", "input": map[string]any{"file_path": "owned-fixture"}}
	if kind == "computer_use_permission_request" {
		value["request"] = map[string]any{"requestId": id, "reason": "owned pending approval"}
	}
	raw, _ := json.Marshal(value)
	o.mu.Lock()
	if _, exists := o.permissions[id]; !exists {
		o.permissionOrder = append(o.permissionOrder, id)
	}
	o.permissions[id] = raw
	o.mu.Unlock()
	o.output(raw)
}
func (o *liveStatusPublicOwner) resolve(id string) {
	o.mu.Lock()
	delete(o.permissions, id)
	o.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"type": "permission_resolved", "requestId": id, "allowed": false})
	o.output(raw)
}
func (o *liveStatusPublicOwner) Snapshot(ctx context.Context) ([]json.RawMessage, error) {
	o.mu.Lock()
	state, background := "idle", []string{}
	if o.decoySnapshot {
		state, background = "running", []string{"snapshot-is-not-a-task-owner"}
	}
	frames := []json.RawMessage{rawDisconnect(map[string]any{"type": "session_state", "turnState": state, "activeBackgroundTaskIds": background})}
	tools, computers := []string{}, []string{}
	for _, id := range o.permissionOrder {
		raw, exists := o.permissions[id]
		if !exists {
			continue
		}
		frames = append(frames, append(json.RawMessage(nil), raw...))
		var request struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &request)
		if request.Type == "permission_request" {
			tools = append(tools, id)
		} else {
			computers = append(computers, id)
		}
	}
	frames = append(frames, rawDisconnect(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": tools, "computerUseRequestIds": computers, "turnActive": o.decoySnapshot}))
	armed, entered, release := o.snapshotArmed, o.snapshotEntered, o.snapshotRelease
	o.snapshotArmed = false
	o.mu.Unlock()
	if armed {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return frames, nil
}
func (o *liveStatusPublicOwner) Handle(ctx context.Context, raw json.RawMessage) error {
	owned := append(json.RawMessage(nil), raw...)
	select {
	case o.received <- owned:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		select {
		case o.handled <- owned:
		default:
		}
	}()
	var input struct {
		Type   string `json:"type"`
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	if input.Type == "user_message" && o.finish != nil {
		select {
		case <-o.finish:
		case <-ctx.Done():
			return ctx.Err()
		}
		o.SDK(`{"type":"result","subtype":"success","is_error":false}`)
		o.output(json.RawMessage(`{"type":"message_complete"}`))
		close(o.result)
		select {
		case <-o.tail:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if input.Type == "stop_background_task" {
		o.mu.Lock()
		result, callback := o.stopResults[input.TaskID], o.onStopped
		o.mu.Unlock()
		if result == "failed" {
			o.output(rawDisconnect(map[string]any{"type": "background_task_stop_failed", "taskId": input.TaskID, "message": "owned Stop rejected"}))
			return nil // Actual DesktopSession also reports this logical failure and returns nil.
		}
		if callback == nil {
			return errors.New("actual OnBackgroundTaskStopped owner callback unavailable")
		}
		callback(input.TaskID, result == "alreadyGone")
	}
	return nil
}
func (o *liveStatusPublicOwner) Close(context.Context) error {
	o.closeOnce.Do(func() { close(o.closed) })
	return nil
}
func (*liveStatusPublicOwner) AcknowledgeTeamWorkerModel(context.Context, string) (protocol.JSONObject, error) {
	return protocol.NewJSONObject(), nil
}
func (*liveStatusPublicOwner) SubmitTeamTask(context.Context, protocol.JSONObject) error { return nil }

func liveStatusPublicAwait(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal(description)
	}
}
func liveStatusPublicHandled(t *testing.T, owner *liveStatusPublicOwner, kind string) {
	t.Helper()
	for count := 0; count < 16; count++ {
		select {
		case raw := <-owner.handled:
			var value struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &value)
			if value.Type == kind {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("owner did not finish control", kind)
		}
	}
	t.Fatal("owner control not found", kind)
}
func liveStatusPublicSend(t *testing.T, socket *websocket.Conn, raw string) {
	t.Helper()
	if err := websocket.Message.Send(socket, raw); err != nil {
		t.Fatal(err)
	}
}
func liveStatusPublicWarm(t *testing.T, host *httptest.Server, id string, owner *liveStatusPublicOwner) *websocket.Conn {
	t.Helper()
	socket := dial(t, host, id)
	receiveType(t, socket, "connected")
	receiveType(t, socket, "permission_requests_snapshot")
	liveStatusPublicSend(t, socket, `{"type":"prewarm_session"}`)
	liveStatusPublicHandled(t, owner, "prewarm_session")
	return socket
}
func liveStatusPublicFetch(t *testing.T, host *httptest.Server) map[string]string {
	t.Helper()
	raw := requestJSON(t, host, "GET", "/api/sessions/live-status", "", 200)
	var wire struct {
		Sessions []struct {
			ID       string `json:"id"`
			Activity string `json:"activityState"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Sessions == nil {
		t.Fatal("live-status must return a sessions array", err, string(raw))
	}
	result := map[string]string{}
	for _, item := range wire.Sessions {
		if item.ID == "" || item.Activity != "running" && item.Activity != "waiting" {
			t.Fatal("invalid live item", string(raw))
		}
		if _, duplicate := result[item.ID]; duplicate {
			t.Fatal("duplicate live session", string(raw))
		}
		result[item.ID] = item.Activity
	}
	return result
}
func liveStatusPublicWant(t *testing.T, host *httptest.Server, expected map[string]string) {
	t.Helper()
	if got := liveStatusPublicFetch(t, host); !reflect.DeepEqual(got, expected) {
		t.Fatalf("live-status=%v want=%v", got, expected)
	}
}
func liveStatusPublicEventually(t *testing.T, host *httptest.Server, expected map[string]string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := liveStatusPublicFetch(t, host)
		if reflect.DeepEqual(got, expected) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("live-status never settled: got=%v want=%v", got, expected)
		}
		time.Sleep(time.Millisecond)
	}
}
func liveStatusPublicChatState(t *testing.T, host *httptest.Server, id, expected string) {
	t.Helper()
	raw := requestJSON(t, host, "GET", "/api/sessions/"+id+"/chat/status", "", 200)
	var wire struct {
		Activity string `json:"activityState"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Activity != expected {
		t.Fatal("chat activity mismatched", err, expected, string(raw))
	}
}

// Canonical bytes are read only from this test's disposable session returned
// by the real server index, never from a user transcript or profile.
type liveStatusPublicTaskNotification struct {
	TaskID    string `json:"taskId"`
	ToolUseID string `json:"toolUseId"`
	Status    string `json:"status"`
}

func liveStatusPublicCanonical(t *testing.T, server *Server, id string) ([]liveStatusPublicTaskNotification, []byte) {
	t.Helper()
	info, err := server.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	notifications := []liveStatusPublicTaskNotification{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row struct {
			Type string          `json:"type"`
			Task json.RawMessage `json:"taskNotification"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal("invalid owned canonical JSONL", err)
		}
		if row.Type != "cc-haha-task-notification" {
			continue
		}
		var notification liveStatusPublicTaskNotification
		if err := json.Unmarshal(row.Task, &notification); err != nil {
			t.Fatal("invalid canonical task notification", err)
		}
		notifications = append(notifications, notification)
	}
	return notifications, data
}
func liveStatusPublicOneStopped(t *testing.T, server *Server, id, taskID, toolUseID string) {
	t.Helper()
	notifications, _ := liveStatusPublicCanonical(t, server, id)
	count := 0
	for _, notification := range notifications {
		if notification.TaskID == taskID {
			count++
			if notification.ToolUseID != toolUseID || notification.Status != "stopped" {
				t.Fatal("canonical stopped identity changed", notification)
			}
		}
	}
	if count != 1 {
		t.Fatal("successful bookend must persist exactly once", taskID, count)
	}
}

func TestLiveStatusPublicCollectionLegacyQueueAndMethod(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) {
		t.Fatal("legacy queue must not start model runtime")
		return nil, nil
	})
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	_ = createTestSession(t, host, options.WorkDir)    // Persisted history alone is not activity.
	liveStatusPublicWant(t, host, map[string]string{}) // Intended old-handler RED: HTTP 404, not a compile error.
	requestJSON(t, host, "POST", "/api/sessions/"+id+"/chat", `{"content":"owned legacy queue"}`, 202)
	liveStatusPublicWant(t, host, map[string]string{id: "running"})
	requestJSON(t, host, "POST", "/api/sessions/"+id+"/chat/stop", "", 200)
	liveStatusPublicWant(t, host, map[string]string{})
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		requestJSON(t, host, method, "/api/sessions/live-status", "{}", 405)
	}
}

func TestLiveStatusPublicRegisteredStartupAndResultBeforeHandleCleanup(t *testing.T) {
	for _, kind := range []string{"user_message", "prewarm_session"} {
		t.Run(kind, func(t *testing.T) {
			owner := newLiveStatusPublicOwner()
			owner.decoySnapshot = true
			owner.finish, owner.tail, owner.result = make(chan struct{}), make(chan struct{}), make(chan struct{})
			entered, release := make(chan struct{}), make(chan struct{})
			options := testOptions(t, func(ctx context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
				owner.attach(o, emit)
				close(entered)
				select {
				case <-release:
					return owner, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			options.AfterRuntimeTimer = (&lifecycleClock{}).after
			_, host := testHost(t, options)
			id := createTestSession(t, host, options.WorkDir)
			socket := dial(t, host, id)
			receiveType(t, socket, "connected")
			receiveType(t, socket, "permission_requests_snapshot")
			liveStatusPublicSend(t, socket, `{"type":"`+kind+`","content":"owned suspended foreground"}`)
			liveStatusPublicAwait(t, entered, "registered input did not enter factory")
			want := map[string]string{}
			if kind == "user_message" {
				want[id] = "running"
			}
			liveStatusPublicWant(t, host, want)
			close(release)
			if kind == "prewarm_session" {
				liveStatusPublicHandled(t, owner, kind)
				liveStatusPublicWant(t, host, map[string]string{})
				return
			}
			select {
			case <-owner.received:
			case <-time.After(3 * time.Second):
				t.Fatal("foreground did not reach owner")
			}
			liveStatusPublicWant(t, host, want)
			close(owner.finish)
			liveStatusPublicAwait(t, owner.result, "owner result was not emitted")
			// Handle is deliberately still suspended on tail. Neither admission
			// cleanup nor the stale Snapshot running/background IDs is activity.
			liveStatusPublicWant(t, host, map[string]string{})
			liveStatusPublicChatState(t, host, id, "idle")
			close(owner.tail)
			liveStatusPublicHandled(t, owner, kind)
		})
	}
}

func TestLiveStatusPublicSDKAndBackgroundLifecycle(t *testing.T) {
	owner := newLiveStatusPublicOwner()
	owner.decoySnapshot = true
	options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
		owner.attach(o, emit)
		return owner, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	liveStatusPublicWarm(t, host, id, owner)
	liveStatusPublicWant(t, host, map[string]string{})
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	liveStatusPublicWant(t, host, map[string]string{id: "running"})
	owner.SDK(`{"type":"result","is_error":false}`)
	liveStatusPublicWant(t, host, map[string]string{id: "running"})
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	liveStatusPublicWant(t, host, map[string]string{})
	owner.SDK(`{"type":"system","subtype":"task_started","task_id":"owned-shell","tool_use_id":"shell-tool","task_type":"local_bash"}`)
	owner.SDK(`{"type":"result","is_error":false}`)
	liveStatusPublicWant(t, host, map[string]string{id: "running"})
	owner.SDK(`{"type":"system","subtype":"task_notification","task_id":"owned-shell","tool_use_id":"shell-tool","status":"completed"}`)
	liveStatusPublicWant(t, host, map[string]string{})
	owner.SDK(`{"type":"system","subtype":"task_progress","task_id":"owned-shell"}`)
	owner.SDK(`{"type":"system","subtype":"task_started","task_id":"owned-shell","task_type":"local_bash"}`)
	liveStatusPublicWant(t, host, map[string]string{})
	owner.SDK(`{"type":"result","is_error":true}`)
	liveStatusPublicChatState(t, host, id, "failed")
	liveStatusPublicWant(t, host, map[string]string{})
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	liveStatusPublicWant(t, host, map[string]string{id: "running"})
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	liveStatusPublicChatState(t, host, id, "failed") // A CLI state event must not erase the terminal failed state.
	owner.SDK(`{"type":"result","is_error":false}`)
	liveStatusPublicChatState(t, host, id, "idle")
}

func TestLiveStatusPublicApprovalPrecedenceAndStopRejectsLateSDK(t *testing.T) {
	owner := newLiveStatusPublicOwner()
	options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
		owner.attach(o, emit)
		return owner, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	socket := liveStatusPublicWarm(t, host, id, owner)
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	owner.permission("owned-write", "permission_request")
	liveStatusPublicWant(t, host, map[string]string{id: "waiting"})
	owner.permission("owned-cu", "computer_use_permission_request")
	owner.resolve("owned-write")
	liveStatusPublicWant(t, host, map[string]string{id: "waiting"})
	owner.resolve("owned-cu")
	liveStatusPublicWant(t, host, map[string]string{id: "running"})
	liveStatusPublicSend(t, socket, `{"type":"stop_generation"}`)
	liveStatusPublicHandled(t, owner, "stop_generation")
	owner.permission("stale-write", "permission_request")
	owner.permission("stale-cu", "computer_use_permission_request")
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	owner.SDK(`{"type":"result","is_error":true}`)
	liveStatusPublicWant(t, host, map[string]string{})
	liveStatusPublicChatState(t, host, id, "idle")
	liveStatusPublicSend(t, socket, `{"type":"user_message","content":"owned next turn"}`)
	liveStatusPublicHandled(t, owner, "user_message")
	liveStatusPublicWant(t, host, map[string]string{id: "waiting"})
	owner.resolve("stale-write")
	owner.resolve("stale-cu")
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	owner.SDK(`{"type":"result","is_error":false}`)
	liveStatusPublicWant(t, host, map[string]string{})
}

func TestLiveStatusPublicActualTeamWorkerExcludedAndToolApprovalRollsUp(t *testing.T) {
	lead, worker := newLiveStatusPublicOwner(), newLiveStatusPublicOwner()
	options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
		owner := lead
		if o.TeamWorker != nil {
			owner = worker
		}
		owner.attach(o, emit)
		return owner, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	liveStatusPublicWarm(t, host, id, lead)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	workerID, err := server.PrepareTeamWorker(ctx, TeamWorkerLaunch{Model: "owned-fixture-model", WorkDir: options.WorkDir, PermissionMode: "default", Worker: teamworker.Start{ParentSessionID: id, MemberID: "member", Name: "member", TeamName: "owned-team", AgentDefinition: protocol.NewJSONObject()}})
	if err != nil {
		t.Fatal(err)
	}
	worker.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	liveStatusPublicWant(t, host, map[string]string{}) // Worker work alone does not make its idle lead running.
	worker.permission("member-write", "permission_request")
	liveStatusPublicWant(t, host, map[string]string{id: "waiting"})
	worker.permission("member-cu", "computer_use_permission_request")
	worker.resolve("member-write")
	liveStatusPublicWant(t, host, map[string]string{}) // Source rolls up child tool approvals, not child CU or busy state.
	if err := server.StopTeamWorker(ctx, workerID); err != nil {
		t.Fatal(err)
	}
	worker.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	worker.permission("retired-member-write", "permission_request")
	liveStatusPublicWant(t, host, map[string]string{})
}

func TestLiveStatusPublicDisconnectedActivityEndsAtActualOwnerClose(t *testing.T) {
	owner := newLiveStatusPublicOwner()
	options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
		owner.attach(o, emit)
		return owner, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	socket := liveStatusPublicWarm(t, host, id, owner)
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	_ = socket.Close()
	liveStatusPublicWant(t, host, map[string]string{id: "running"})
	owner.mu.Lock()
	closed, oldStopped := owner.onClosed, owner.onStopped
	owner.mu.Unlock()
	if closed == nil || oldStopped == nil {
		t.Fatal("actual runtime retirement/Stop callback unavailable")
	}
	if err := closed(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	liveStatusPublicAwait(t, owner.closed, "retirement did not join the exact owner Close")
	liveStatusPublicWant(t, host, map[string]string{})
	liveStatusPublicChatState(t, host, id, "idle")
	_, before := liveStatusPublicCanonical(t, server, id)
	oldStopped("retired-unknown-task", true)
	_, after := liveStatusPublicCanonical(t, server, id)
	if !bytes.Equal(before, after) {
		t.Fatal("retired generation Stop callback wrote an unknown fallback bookend")
	}
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	owner.permission("retired-write", "permission_request")
	liveStatusPublicWant(t, host, map[string]string{})
}

// This gate blocks only the existing PersistSession callback after the owner
// control is enabled. Returning nil permits the normal append to the disposable
// canonical transcript; it is not a replacement persistence implementation.
type liveStatusPublicPersistenceGate struct {
	active  atomic.Bool
	entered chan struct{}
	answers chan error
	once    sync.Once
}

func (g *liveStatusPublicPersistenceGate) persist(ctx context.Context) (bool, error) {
	if !g.active.Load() {
		return true, nil
	}
	select {
	case g.entered <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case err := <-g.answers:
		return err == nil, err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
func (g *liveStatusPublicPersistenceGate) close() {
	g.active.Store(false)
	g.once.Do(func() { close(g.answers) })
}

func TestLiveStatusPublicPerTaskStopStrictBookendAndRetryWindow(t *testing.T) {
	for _, kind := range []string{"local_agent", "local_bash"} {
		t.Run(kind, func(t *testing.T) {
			owner := newLiveStatusPublicOwner()
			gate := &liveStatusPublicPersistenceGate{entered: make(chan struct{}, 8), answers: make(chan error)}
			clock := &lifecycleClock{}
			options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
				owner.attach(o, emit)
				return owner, nil
			})
			options.PersistSession, options.AfterRuntimeTimer = gate.persist, clock.after
			server, host := testHost(t, options)
			t.Cleanup(gate.close) // Registered after host cleanup, so blocked writes are released before Close joins them.
			id := createTestSession(t, host, options.WorkDir)
			socket := liveStatusPublicWarm(t, host, id, owner)
			owner.mu.Lock()
			callbackPresent := owner.onStopped != nil
			owner.stopResults["owned-task"] = "failed"
			owner.mu.Unlock()
			if !callbackPresent {
				t.Fatal("success-only background Stop callback not wired into actual Factory")
			}
			owner.SDK(`{"type":"system","subtype":"task_started","task_id":"owned-task","tool_use_id":"owned-tool","task_type":"` + kind + `"}`)
			liveStatusPublicSend(t, socket, `{"type":"stop_background_task","taskId":"owned-task"}`)
			liveStatusPublicHandled(t, owner, "stop_background_task")
			liveStatusPublicWant(t, host, map[string]string{id: "running"}) // Handle(nil) after a logical Stop failure is not success.
			owner.mu.Lock()
			owner.stopResults["owned-task"] = "success"
			owner.mu.Unlock()
			gate.active.Store(true)
			liveStatusPublicSend(t, socket, `{"type":"stop_background_task","taskId":"owned-task"}`)
			pending := map[string]string{}
			if kind == "local_agent" {
				pending[id] = "running"
			}
			for attempt := 0; attempt < 3; attempt++ {
				liveStatusPublicAwait(t, gate.entered, "strict bookend did not enter real persistence")
				liveStatusPublicWant(t, host, pending)
				gate.answers <- errors.New("owned persistence failure")
			}
			liveStatusPublicHandled(t, owner, "stop_background_task")
			liveStatusPublicEventually(t, host, map[string]string{}) // Retry timer waiting is not Agent bookendPending.
			timer := clock.latest(t)
			if timer.delay != 250*time.Millisecond {
				t.Fatal("missing first owned finalization retry", timer.delay)
			}
			retried := make(chan struct{})
			go func() { timer.callback(); close(retried) }()
			liveStatusPublicAwait(t, gate.entered, "owned retry did not enter persistence")
			liveStatusPublicWant(t, host, pending)
			// The real append checks persistence policy again after opening the
			// transcript. Release this first check and let that second check use
			// the normal callback result rather than accidentally blocking twice.
			gate.active.Store(false)
			gate.answers <- nil
			liveStatusPublicAwait(t, retried, "owned retry did not settle")
			liveStatusPublicWant(t, host, map[string]string{})
			liveStatusPublicOneStopped(t, server, id, "owned-task", "owned-tool")
			owner.SDK(`{"type":"system","subtype":"task_started","task_id":"owned-task","task_type":"` + kind + `"}`)
			owner.SDK(`{"type":"system","subtype":"task_progress","task_id":"owned-task"}`)
			liveStatusPublicWant(t, host, map[string]string{})
		})
	}
}

func TestLiveStatusPublicPerTaskStopLeavesOtherTaskAndAcceptsAlreadyGone(t *testing.T) {
	owner := newLiveStatusPublicOwner()
	options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
		owner.attach(o, emit)
		return owner, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	socket := liveStatusPublicWarm(t, host, id, owner)
	owner.mu.Lock()
	present := owner.onStopped != nil
	owner.stopResults["first"] = "alreadyGone"
	owner.mu.Unlock()
	if !present {
		t.Fatal("success-only background Stop callback unavailable")
	}
	owner.SDK(`{"type":"system","subtype":"task_started","task_id":"first","tool_use_id":"first-tool","task_type":"local_agent"}`)
	owner.SDK(`{"type":"system","subtype":"task_started","task_id":"second","tool_use_id":"second-tool","task_type":"local_bash"}`)
	liveStatusPublicSend(t, socket, `{"type":"stop_background_task","taskId":"first"}`)
	liveStatusPublicHandled(t, owner, "stop_background_task")
	liveStatusPublicWant(t, host, map[string]string{id: "running"})
	owner.SDK(`{"type":"system","subtype":"task_notification","task_id":"second","tool_use_id":"second-tool","status":"completed"}`)
	liveStatusPublicWant(t, host, map[string]string{})
	owner.SDK(`{"type":"system","subtype":"task_started","task_id":"first","task_type":"local_agent"}`)
	liveStatusPublicWant(t, host, map[string]string{})
	liveStatusPublicOneStopped(t, server, id, "first", "first-tool")
	owner.mu.Lock()
	owner.stopResults["never-tracked-evicted-task"] = "alreadyGone"
	owner.mu.Unlock()
	liveStatusPublicSend(t, socket, `{"type":"stop_background_task","taskId":"never-tracked-evicted-task"}`)
	liveStatusPublicHandled(t, owner, "stop_background_task")
	liveStatusPublicWant(t, host, map[string]string{})
	liveStatusPublicOneStopped(t, server, id, "never-tracked-evicted-task", "never-tracked-evicted-task")
}

// This RED is independently runnable on the old public endpoints: it does not
// call the new collection or require the new background Stop callback.
func TestConversationStatusPublicStopKeepsLateSDKInterrupted(t *testing.T) {
	owner := newLiveStatusPublicOwner()
	options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
		owner.attach(o, emit)
		return owner, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	socket := liveStatusPublicWarm(t, host, id, owner)
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	liveStatusPublicChatState(t, host, id, "running")
	owner.permission("late-sdk-retained-write", "permission_request")
	liveStatusPublicChatState(t, host, id, "waiting")
	liveStatusPublicSend(t, socket, `{"type":"stop_generation"}`)
	liveStatusPublicHandled(t, owner, "stop_generation")
	liveStatusPublicChatState(t, host, id, "idle")
	owner.SDK(`{"type":"system","subtype":"session_state_changed","state":"running"}`)
	liveStatusPublicChatState(t, host, id, "idle") // Old RunState clears Interrupted and incorrectly returns waiting.
}

func TestLiveStatusPublicResumeWorkerWithoutLeadDoesNotRollUpRequests(t *testing.T) {
	worker := newLiveStatusPublicOwner()
	options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
		worker.attach(o, emit)
		return worker, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	server, host := testHost(t, options)
	leadID := createTestSession(t, host, options.WorkDir) // Real saved lead, no live lead registry entry.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	workerID, err := server.PrepareTeamWorker(ctx, TeamWorkerLaunch{Resume: true, Model: "owned-fixture-model", WorkDir: options.WorkDir, Worker: teamworker.Start{ParentSessionID: leadID, MemberID: "orphan-member", Name: "orphan-member", TeamName: "owned-team", AgentDefinition: protocol.NewJSONObject()}})
	if err != nil {
		t.Fatal(err)
	}
	worker.permission("orphan-member-write", "permission_request")
	liveStatusPublicChatState(t, host, leadID, "idle")
	liveStatusPublicWant(t, host, map[string]string{})
	requestJSON(t, host, "POST", "/api/sessions/"+leadID+"/chat", `{"content":"owned legacy queue without live lead"}`, 202)
	liveStatusPublicChatState(t, host, leadID, "running")
	liveStatusPublicWant(t, host, map[string]string{leadID: "running"})
	if err := server.StopTeamWorker(ctx, workerID); err != nil {
		t.Fatal(err)
	}
}

func TestLiveStatusPublicChildSnapshotCannotResurrectRetiredWorkerApproval(t *testing.T) {
	lead, worker := newLiveStatusPublicOwner(), newLiveStatusPublicOwner()
	options := testOptions(t, func(_ context.Context, o SessionOptions, emit Emit) (LiveSession, error) {
		owner := lead
		if o.TeamWorker != nil {
			owner = worker
		}
		owner.attach(o, emit)
		return owner, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	server, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	liveStatusPublicWarm(t, host, id, lead)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	workerID, err := server.PrepareTeamWorker(ctx, TeamWorkerLaunch{Model: "owned-fixture-model", WorkDir: options.WorkDir, Worker: teamworker.Start{ParentSessionID: id, MemberID: "retiring-member", Name: "retiring-member", TeamName: "owned-team", AgentDefinition: protocol.NewJSONObject()}})
	if err != nil {
		t.Fatal(err)
	}
	worker.permission("snapshot-before-retirement", "permission_request")
	liveStatusPublicWant(t, host, map[string]string{id: "waiting"})
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	worker.mu.Lock()
	worker.snapshotEntered, worker.snapshotRelease, worker.snapshotArmed = entered, release, true
	worker.mu.Unlock()
	type response struct {
		code int
		data []byte
		err  error
	}
	completed := make(chan response, 1)
	go func() {
		res, err := host.Client().Get(host.URL + "/api/sessions/live-status")
		if err != nil {
			completed <- response{err: err}
			return
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		completed <- response{code: res.StatusCode, data: data, err: err}
	}()
	liveStatusPublicAwait(t, entered, "public GET did not reach the actual child pending-request snapshot")
	if err := server.StopTeamWorker(ctx, workerID); err != nil {
		t.Fatal(err)
	}
	liveStatusPublicAwait(t, worker.closed, "actual child Close was not joined")
	close(release) // Return the exact pre-Close permission bytes to the in-flight GET.
	select {
	case result := <-completed:
		if result.err != nil || result.code != 200 {
			t.Fatal("in-flight live-status failed", result.code, result.err, string(result.data))
		}
		var wire struct {
			Sessions []json.RawMessage `json:"sessions"`
		}
		if err := json.Unmarshal(result.data, &wire); err != nil || wire.Sessions == nil || len(wire.Sessions) != 0 {
			t.Fatal("retired worker Snapshot resurrected waiting", err, string(result.data))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("public GET did not join after the child snapshot was released")
	}
	liveStatusPublicWant(t, host, map[string]string{})
}
