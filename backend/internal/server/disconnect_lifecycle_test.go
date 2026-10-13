package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type lifecycleTimer struct {
	delay    time.Duration
	callback func()
	stopped  atomic.Bool
}

func (t *lifecycleTimer) Stop() bool { return !t.stopped.Swap(true) }

type lifecycleClock struct {
	mu     sync.Mutex
	timers []*lifecycleTimer
}

func (c *lifecycleClock) after(delay time.Duration, f func()) RuntimeTimer {
	t := &lifecycleTimer{delay: delay, callback: f}
	c.mu.Lock()
	c.timers = append(c.timers, t)
	c.mu.Unlock()
	return t
}
func (c *lifecycleClock) latest(t *testing.T) *lifecycleTimer {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.timers) == 0 {
		t.Fatal("no runtime timeout")
	}
	return c.timers[len(c.timers)-1]
}

type lifecycleSession struct {
	running atomic.Bool
	closed  atomic.Int32
}
type lostLifecycleSession struct {
	lifecycleSession
	alive atomic.Bool
}

func (s *lostLifecycleSession) RuntimeAlive() bool { return s.alive.Load() }

func TestDisconnectActualRuntimeLossPersistsFailedAndRejectsLateTerminal(t *testing.T) {
	session := &lostLifecycleSession{}
	session.alive.Store(true)
	var sdk func(json.RawMessage)
	var emit Emit
	options := testOptions(t, func(_ context.Context, o SessionOptions, e Emit) (LiveSession, error) {
		sdk = o.OnSDK
		emit = e
		return session, nil
	})
	options.AfterRuntimeTimer = (&lifecycleClock{}).after
	s, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	if _, err := s.live(id); err != nil {
		t.Fatal(err)
	}
	sdk(json.RawMessage(`{"type":"system","subtype":"task_started","task_id":"lost-shell","tool_use_id":"lost-tool","task_type":"local_bash"}`))
	r, _ := s.runtime(id)
	snapshot := s.disconnectSnapshot(r, []json.RawMessage{json.RawMessage(`{"type":"session_state","turnState":"running"}`)})
	if !strings.Contains(string(snapshot[0]), `"activeBackgroundTaskIds":["lost-shell"]`) {
		t.Fatal("reconnect lost task IDs", string(snapshot[0]))
	}
	session.alive.Store(false)
	// This model result is not the liveness proof. The native engine's explicit
	// RuntimeAlive boundary is false before it arrives.
	sdk(json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true}`))
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		done := r.live == nil && r.closing == nil
		r.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lost runtime close wasn't joined")
		}
		time.Sleep(time.Millisecond)
	}
	info, err := s.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status":"failed"`) {
		t.Fatal("loss had no failed durable bookend", string(data))
	}
	size := len(data)
	emit(json.RawMessage(`{"type":"system_notification","subtype":"task_notification","data":{"type":"system","subtype":"task_notification","task_id":"lost-shell","tool_use_id":"lost-tool","status":"completed"}}`))
	sdk(json.RawMessage(`{"type":"system","subtype":"task_progress","task_id":"lost-shell"}`))
	data, _ = os.ReadFile(info.Path)
	if len(data) != size {
		t.Fatal("retired runtime overwrote failure")
	}
}

func TestDisconnectFailedBookendRetainsProofRetriesAndJoinsOnClose(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "shutdown-cancels-pending-retry", true: "storage-recovers"}[restore], func(t *testing.T) {
			clock := &lifecycleClock{}
			session := &lifecycleSession{}
			var sdk func(json.RawMessage)
			options := testOptions(t, func(_ context.Context, o SessionOptions, _ Emit) (LiveSession, error) {
				sdk = o.OnSDK
				return session, nil
			})
			var failing atomic.Bool
			var failures atomic.Int32
			options.PersistSession = func(context.Context) (bool, error) {
				if failing.Load() {
					return false, errors.New("fixture persistence policy unavailable")
				}
				return true, nil
			}
			options.OnUnhandled = func(httpapi.UnhandledError) { failures.Add(1) }
			options.AfterRuntimeTimer = clock.after
			s, host := testHost(t, options)
			id := createTestSession(t, host, options.WorkDir)
			if _, err := s.live(id); err != nil {
				t.Fatal(err)
			}
			sdk(json.RawMessage(`{"type":"system","subtype":"task_started","task_id":"retry-task","tool_use_id":"retry-tool","task_type":"local_bash"}`))
			failing.Store(true)
			clock.latest(t).callback()
			if failures.Load() != 3 {
				t.Fatal("authoritative stop did not make exactly three initial persistence attempts", failures.Load())
			}
			r, _ := s.runtime(id)
			r.mu.Lock()
			d := r.disconnectState()
			pending := d.tasks["retry-task"].finalStatus == "failed" && !d.terminal["retry-task"] && d.finalizationScheduled
			r.mu.Unlock()
			if !pending {
				t.Fatal("failed write discarded its authoritative stop proof")
			}
			if !restore {
				done := make(chan error, 1)
				go func() { done <- s.Close(context.Background()) }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("shutdown didn't cancel/join its retry")
				}
				return
			}
			failing.Store(false)
			deadline := time.Now().Add(2 * time.Second)
			for {
				r.mu.Lock()
				saved := d.terminal["retry-task"] && !d.finalizationScheduled
				r.mu.Unlock()
				if saved {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("same task was not durably finalized after storage recovered")
				}
				time.Sleep(5 * time.Millisecond)
			}
			info, err := s.findSession(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(info.Path)
			if err != nil || !strings.Contains(string(data), `"status":"failed"`) {
				t.Fatal("retry did not append canonical task notification", err, string(data))
			}
		})
	}
}

func (s *lifecycleSession) Handle(context.Context, json.RawMessage) error { return nil }
func (s *lifecycleSession) Snapshot(context.Context) ([]json.RawMessage, error) {
	state := "idle"
	if s.running.Load() {
		state = "running"
	}
	return []json.RawMessage{rawDisconnect(map[string]any{"type": "session_state", "turnState": state})}, nil
}
func (s *lifecycleSession) Close(context.Context) error {
	s.closed.Add(1)
	s.running.Store(false)
	return nil
}
func lifecycleMessage(raw string) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON([]byte(raw))
	return out
}

func TestDisconnectRootBackgroundPredicateExclusions(t *testing.T) {
	cases := []struct {
		name                                                          string
		pending, active, team, cli, wait, agent, permission, computer bool
		want                                                          string
	}{
		{"shell-after-root-result", true, true, false, true, true, false, false, false, "background"},
		{"headless-shell", false, false, false, false, false, false, false, false, "background"},
		{"foreground-followup", true, true, false, true, false, false, false, false, ""},
		{"agent-task", false, false, false, false, false, true, false, false, ""},
		{"team-active-upstream", false, false, true, false, false, false, false, false, ""},
		{"tool-permission-wins-running", true, true, false, true, false, false, true, false, "permission"},
		{"computer-permission-excludes-shell-only", false, false, false, false, false, false, false, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := (&runtimeSession{}).disconnectState()
			d.cliRunning = tc.cli
			d.rootWaiting = tc.wait
			d.tasks["task"] = disconnectTask{agent: tc.agent}
			if tc.permission {
				d.tool["p"] = true
			}
			if tc.computer {
				d.computer["c"] = true
			}
			if got := d.decision(tc.pending, tc.active, tc.team, false); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDisconnectPhysicalTimerRootFollowupAndQueuedCanceledCallbacks(t *testing.T) {
	clock := &lifecycleClock{}
	session := &lifecycleSession{}
	var sdk func(json.RawMessage)
	var emit Emit
	options := testOptions(t, func(_ context.Context, o SessionOptions, e Emit) (LiveSession, error) {
		sdk = o.OnSDK
		emit = e
		return session, nil
	})
	options.AfterRuntimeTimer = clock.after
	s, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	if _, err := s.live(id); err != nil {
		t.Fatal(err)
	}
	idle := clock.latest(t)
	session.running.Store(true)
	sdk(json.RawMessage(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	sdk(json.RawMessage(`{"type":"system","subtype":"task_started","task_id":"bash","tool_use_id":"tool-bash","task_type":"local_bash"}`))
	sdk(json.RawMessage(`{"type":"result","subtype":"success"}`))
	first := clock.latest(t)
	if first.delay != 31*time.Minute {
		t.Fatal(first.delay)
	}
	sdk(json.RawMessage(`{"type":"system","subtype":"task_progress","task_id":"bash","task_type":"local_bash"}`))
	if clock.latest(t) != first {
		t.Fatal("progress restarted the shell ceiling")
	}
	sdk(json.RawMessage(`{"type":"system","subtype":"init"}`))
	first.callback()
	idle.callback()
	if session.closed.Load() != 0 {
		t.Fatal("canceled timeout closed foreground followup")
	}
	sdk(json.RawMessage(`{"type":"result","subtype":"success"}`))
	second := clock.latest(t)
	if second == first {
		t.Fatal("new root result didn't start a fresh ceiling")
	}
	// Reconnect cancels this exact timer. A queued callback can still run and
	// must not close the shared runtime even after a subsequent disconnect.
	s.mu.Lock()
	socket := &connection{}
	s.connections[id] = map[*connection]struct{}{socket: {}}
	s.mu.Unlock()
	s.armDisconnect(id)
	s.mu.Lock()
	delete(s.connections[id], socket)
	s.mu.Unlock()
	s.armDisconnect(id)
	third := clock.latest(t)
	second.callback()
	if session.closed.Load() != 0 {
		t.Fatal("queued timeout from previous disconnect won")
	}
	third.callback()
	if session.closed.Load() != 1 {
		t.Fatal("shell ceiling did not close actual factory session")
	}
	info, err := s.findSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"cc-haha-task-notification"`) || !strings.Contains(string(data), `"status":"failed"`) {
		t.Fatal("no durable runtime-exit failure bookend", string(data))
	}
	before := len(data)
	// Both old renderer and SDK callbacks are generation-fenced after Close.
	emit(json.RawMessage(`{"type":"system_notification","subtype":"task_progress","data":{"type":"system","subtype":"task_progress","task_id":"bash"}}`))
	sdk(json.RawMessage(`{"type":"system","subtype":"task_notification","task_id":"bash","tool_use_id":"tool-bash","status":"completed"}`))
	data, _ = os.ReadFile(info.Path)
	if len(data) != before {
		t.Fatal("late task completion overwrote stopped bookend")
	}
}

func TestDisconnectPermissionCeilingAndMigrationFence(t *testing.T) {
	clock := &lifecycleClock{}
	session := &lifecycleSession{}
	var sdk func(json.RawMessage)
	var emit Emit
	options := testOptions(t, func(_ context.Context, o SessionOptions, e Emit) (LiveSession, error) {
		sdk = o.OnSDK
		emit = e
		return session, nil
	})
	options.AfterRuntimeTimer = clock.after
	s, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	_, err := s.live(id)
	if err != nil {
		t.Fatal(err)
	}
	session.running.Store(true)
	sdk(json.RawMessage(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	emit(json.RawMessage(`{"type":"permission_request","requestId":"p","toolName":"Bash"}`))
	permission := clock.latest(t)
	if permission.delay != 31*time.Minute {
		t.Fatal(permission.delay)
	}
	s.mu.Lock()
	s.migration.active = true
	s.mu.Unlock()
	permission.callback()
	if session.closed.Load() != 0 {
		t.Fatal("migration maintenance killed runtime")
	}
	s.mu.Lock()
	s.migration.active = false
	s.mu.Unlock()
	emit(json.RawMessage(`{"type":"permission_resolved","requestId":"p"}`))
	permission.callback()
	if session.closed.Load() != 0 {
		t.Fatal("answered permission's old timer killed active turn")
	}
	emit(json.RawMessage(`{"type":"permission_request","requestId":"p2","toolName":"Bash"}`))
	clock.latest(t).callback()
	if session.closed.Load() != 1 {
		t.Fatal("permission deadline did not bound a running model")
	}
}

func TestDisconnectExpiredStartupWaitsAndReconnectSupersedes(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "close-after-startup", true: "reconnect-keeps-runtime"}[reconnect], func(t *testing.T) {
			clock := &lifecycleClock{}
			session := &lifecycleSession{}
			started := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan error, 1)
			options := testOptions(t, func(_ context.Context, _ SessionOptions, _ Emit) (LiveSession, error) {
				close(started)
				<-release
				return session, nil
			})
			options.AfterRuntimeTimer = clock.after
			s, host := testHost(t, options)
			id := createTestSession(t, host, options.WorkDir)
			go func() { _, err := s.live(id); finished <- err }()
			<-started
			s.armDisconnect(id)
			timeout := clock.latest(t)
			timeout.callback()
			if session.closed.Load() != 0 {
				t.Fatal("timeout closed an unpublished runtime")
			}
			if reconnect {
				defer func() { s.mu.Lock(); delete(s.connections, id); s.mu.Unlock() }()
				s.mu.Lock()
				s.connections[id] = map[*connection]struct{}{&connection{}: {}}
				s.mu.Unlock()
				s.armDisconnect(id)
			}
			close(release)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			deadline := time.After(time.Second)
			for !reconnect && session.closed.Load() == 0 {
				select {
				case <-deadline:
					t.Fatal("expired startup decision disappeared")
				case <-time.After(time.Millisecond):
				}
			}
			if reconnect {
				timeout.callback()
				if session.closed.Load() != 0 {
					t.Fatal("reconnect did not supersede startup cleanup")
				}
			}
		})
	}
}
