package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/petaccess"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncollaboration"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionreference"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"

	"golang.org/x/net/websocket"
)

var sessionIDPattern = regexp.MustCompile(`^[0-9a-zA-Z_-]{1,64}$`)

type connection struct {
	pet bool
	ws  *websocket.Conn
	mu  sync.Mutex
}

type admissionFrame struct {
	socket   *connection
	raw      json.RawMessage
	ctx      context.Context
	userTurn *registeredUserTurn
}

func (socket *connection) send(raw json.RawMessage) error {
	if socket.pet {
		raw = petaccess.ServerMessage(raw)
		if raw == nil {
			return nil
		}
	}
	socket.mu.Lock()
	defer socket.mu.Unlock()
	return websocket.Message.Send(socket.ws, string(raw))
}
func (socket *connection) object(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return socket.send(raw)
}
func (socket *connection) failure(message, code string) {
	_ = socket.object(map[string]string{"type": "error", "message": message, "code": code})
}

func (server *Server) websocket(writer http.ResponseWriter, request *http.Request) {
	id := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
	if !sessionIDPattern.MatchString(id) && !(isSideChatID(id) && sessionIDPattern.MatchString(strings.TrimPrefix(id, "side-"))) {
		http.Error(writer, "Invalid session ID", 400)
		return
	}
	websocket.Server{Handshake: func(_ *websocket.Config, _ *http.Request) error { return nil }, Handler: func(ws *websocket.Conn) { server.connect(id, ws, isPetRequest(request)) }}.ServeHTTP(writer, request)
}

func (server *Server) connect(id string, ws *websocket.Conn, kinds ...bool) {
	socket := &connection{ws: ws, pet: len(kinds) > 0 && kinds[0]}
	server.connectSocket(id, socket, nil)
}

func (server *Server) connectSocket(id string, socket *connection, messageAllowed func() bool) {
	ws := socket.ws
	server.mu.Lock()
	if server.closed || server.migration.active || server.deleting[id] {
		server.mu.Unlock()
		_ = ws.Close()
		return
	}
	if server.connections[id] == nil {
		server.connections[id] = make(map[*connection]struct{})
	}
	server.connections[id][socket] = struct{}{}
	runtime := server.runtimes[id]
	var previousPet *connection
	if socket.pet {
		previousPet = server.activePet
		server.activePet = socket
	}
	server.mu.Unlock()
	if previousPet != nil && previousPet != socket {
		previousPet.closePetSwitched()
	}

	if runtime != nil {
		server.cancelDisconnect(runtime)
		runtime.mu.Lock()
		runtime.epoch++
		if runtime.timer != nil {
			runtime.timer.Stop()
			runtime.timer = nil
		}
		runtime.mu.Unlock()
	}
	defer func() {
		server.mu.Lock()
		delete(server.connections[id], socket)
		if server.activePet == socket {
			server.activePet = nil
		}
		server.mu.Unlock()
		_ = ws.Close()
		server.armDisconnect(id)
	}()
	_ = socket.object(map[string]string{"type": "connected", "sessionId": id})
	server.sendSnapshot(id, socket, false)
	for {
		var raw string
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			return
		}
		if messageAllowed != nil && !messageAllowed() {
			return
		}
		var frame struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(raw), &frame) != nil {
			socket.failure("Invalid message format", "PARSE_ERROR")
			continue
		}
		if socket.pet && !petaccess.ClientMessageAllowed(json.RawMessage(raw)) {
			socket.failure("Message type "+frame.Type+" is not available to the pet window", "PET_CAPABILITY_DENIED")
			continue
		}
		switch frame.Type {
		case "ping":
			_ = socket.object(map[string]string{"type": "pong"})
		case "sync_state":
			server.sendSnapshot(id, socket, true)
		case "user_message", "prewarm_session":
			server.enqueue(id, socket, json.RawMessage(raw))
		case "permission_response", "computer_use_permission_response", "set_permission_mode", "set_runtime_config", "stop_generation", "ask_user_question_activity", "stop_background_task":
			if frame.Type == "permission_response" && server.forwardPlanRuntimeApproval(id, socket, json.RawMessage(raw)) {
				continue
			}
			server.forward(id, socket, json.RawMessage(raw))
		default:
			socket.failure("Unknown message type: "+frame.Type, "UNKNOWN_TYPE")
		}
	}
}

func (server *Server) forwardPlanRuntimeApproval(id string, socket *connection, raw json.RawMessage) bool {
	runtime, err := server.runtime(id)
	if err != nil {
		return false
	}
	live, err := server.liveRuntime(id, runtime)
	if err != nil {
		return false
	}
	approval, ok := live.(PlanRuntimeApprovalSession)
	if !ok || !approval.IsPlanRuntimeApproval(raw) {
		return false
	}
	server.mu.Lock()
	if server.closed || server.runtimes[id] != runtime {
		server.mu.Unlock()
		return true
	}
	// The same server-owned operation ledger is canceled and joined by Close.
	// Capture this exact runtime; a later same-SID replacement cannot receive
	// the old connection's approval continuation.
	server.apiOperations.Add(1)
	server.mu.Unlock()
	go func() {
		defer server.apiOperations.Done()
		server.forwardRuntime(id, runtime, socket, append(json.RawMessage(nil), raw...))
	}()
	return true
}

func (server *Server) runtime(id string) (*runtimeSession, error) {
	server.mu.Lock()
	if server.closed || server.migration.active || server.deleting[id] {
		server.mu.Unlock()
		return nil, context.Canceled
	}
	if value := server.runtimes[id]; value != nil {
		server.mu.Unlock()
		return value, nil
	}
	server.mu.Unlock()
	info, err := server.findSession(server.ctx, id)
	if err != nil {
		return nil, err
	}
	runtimeContext, cancel := context.WithCancel(server.ctx)
	value := &runtimeSession{ctx: runtimeContext, cancel: cancel, options: server.sessionOptions(info), wake: make(chan struct{}, 1), admitted: make(chan struct{})}
	value.disconnectState().afterClose = func(err error) { server.flushClosingTaskNotifications(id, value, err) }
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed || server.migration.active || server.deleting[id] {
		cancel()
		return nil, context.Canceled
	}
	if existing := server.runtimes[id]; existing != nil {
		cancel()
		return existing, nil
	}
	server.runtimes[id] = value
	context.AfterFunc(runtimeContext, func() { server.cancelDisconnect(value) })
	go server.admit(id, value)
	return value, nil
}

// The socket reader records user frames synchronously in arrival order. One
// worker admits them to the shared session; controls bypass this queue so an
// active turn can still receive permission replies and cancellation.
func (server *Server) enqueue(id string, socket *connection, raw json.RawMessage) {
	var message protocol.JSONObject
	_ = message.UnmarshalJSON(raw)
	if message.String("type") == "prewarm_session" {
		// Source defers repository checkout until the first real user turn.
		// Read current launch metadata before creating or touching a runtime.
		if info, err := server.findSession(server.ctx, id); err == nil {
			var metadata protocol.JSONObject
			if metadata.UnmarshalJSON(info.Metadata) == nil && sourceJSONTruthy(metadata.Get("repository")) {
				return
			}
		}
	}
	runtime, err := server.runtime(id)
	if err != nil {
		socket.failure(err.Error(), "USER_TURN_FAILED")
		return
	}
	server.mu.Lock()
	runtime.mu.Lock()
	if server.closed || server.migration.active || runtime.retired || runtime.ctx.Err() != nil {
		runtime.mu.Unlock()
		server.mu.Unlock()
		return
	}
	if message.String("type") == "user_message" {
		server.conversations.Begin(id)
	}
	var userTurn *registeredUserTurn
	if message.String("type") == "user_message" {
		userTurn = registerUserTurn(runtime)
	}
	inputContext := runtime.ctx
	if userTurn != nil {
		inputContext = userTurn.ctx
	}
	if input, ok := runtime.live.(PlanRuntimeApprovalSession); ok && message.String("type") == "user_message" {
		inputContext = input.CaptureRuntimeInput(inputContext)
	}
	runtime.disconnectState().cancelTimer()
	runtime.disconnectState().rootActive = message.String("type") == "user_message"
	runtime.disconnectState().rootWaiting = false
	runtime.admission = append(runtime.admission, admissionFrame{socket: socket, raw: append(json.RawMessage(nil), raw...), ctx: inputContext, userTurn: userTurn})
	runtime.epoch++
	if runtime.timer != nil {
		runtime.timer.Stop()
		runtime.timer = nil
	}
	runtime.mu.Unlock()
	server.mu.Unlock()
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
}

func (server *Server) admit(id string, runtime *runtimeSession) {
	defer close(runtime.admitted)
	for {
		runtime.mu.Lock()
		if runtime.retired || runtime.ctx.Err() != nil {
			runtime.admission = nil
			runtime.admitting = false
			runtime.mu.Unlock()
			return
		}
		if len(runtime.admission) == 0 {
			runtime.admitting = false
			runtime.mu.Unlock()
			select {
			case <-runtime.wake:
			case <-runtime.ctx.Done():
			}
			continue
		}
		frame := runtime.admission[0]
		runtime.admission[0] = admissionFrame{}
		runtime.admission = runtime.admission[1:]
		runtime.admitting = true
		runtime.activeUserTurn = frame.userTurn
		runtime.mu.Unlock()
		if frame.ctx == nil || frame.ctx.Err() == nil {
			server.forwardRuntime(id, runtime, frame.socket, frame.raw, frame.ctx)
		}
		runtime.mu.Lock()
		settleRegisteredUserTurn(runtime, frame.userTurn)
		if frame.userTurn != nil {
			frame.userTurn.cancel()
		}
		if runtime.activeUserTurn == frame.userTurn {
			runtime.activeUserTurn = nil
		}
		runtime.admitting = false
		runtime.mu.Unlock()
		server.armDisconnect(id)
	}
}

func (server *Server) live(id string) (LiveSession, error) {
	runtime, err := server.runtime(id)
	if err != nil {
		return nil, err
	}
	return server.liveRuntime(id, runtime)
}

func (server *Server) liveRuntime(id string, runtime *runtimeSession) (LiveSession, error) {
	for {
		runtime.mu.Lock()
		if runtime.retired || runtime.ctx.Err() != nil {
			runtime.mu.Unlock()
			return nil, context.Canceled
		}
		if runtime.closing != nil {
			closing := runtime.closing
			runtime.mu.Unlock()
			select {
			case <-closing:
			case <-runtime.ctx.Done():
				return nil, runtime.ctx.Err()
			}
			continue
		}
		if runtime.live != nil {
			live := runtime.live
			runtime.mu.Unlock()
			return live, nil
		}
		if runtime.opening != nil {
			opening := runtime.opening
			runtime.mu.Unlock()
			select {
			case <-opening:
			case <-server.ctx.Done():
				return nil, server.ctx.Err()
			}
			runtime.mu.Lock()
			err := runtime.err
			runtime.mu.Unlock()
			if err != nil {
				return nil, err
			}
			continue
		}
		runtime.opening = make(chan struct{})
		opening := runtime.opening
		runtime.mu.Unlock()
		generation := server.beginDisconnectGeneration(runtime)
		// Every reopened runtime reloads persisted metadata/history. The previous
		// Agent may have appended turns or runtime overrides before eviction.
		runtime.mu.Lock()
		options := runtime.options
		runtime.mu.Unlock()
		var info sessionInfo
		var err error
		if options.TeamWorker == nil {
			info, err = server.findSession(runtime.ctx, id)
		}
		var live LiveSession
		var revokeCollaboration func()
		if err == nil {
			if options.TeamWorker == nil {
				options = server.sessionOptions(info)
			}
			revokeCollaboration, err = server.bindCollaborationSession(&options)
			if err == nil {
				err = server.prepareSideChatStart(id)
			}
			if err == nil {
				// This factory attempt owns the live generation. Disconnect grace
				// epochs also advance on ordinary input/reconnect, so they cannot
				// stand in for the original active-client/runtime identity check.
				options.CanContinueRuntime = func() bool {
					server.mu.Lock()
					owned := !server.closed && !server.migration.active && server.runtimes[id] == runtime && len(server.connections[id]) > 0
					server.mu.Unlock()
					if !owned {
						return false
					}
					runtime.mu.Lock()
					defer runtime.mu.Unlock()
					return !runtime.retired && runtime.ctx.Err() == nil && runtime.live == live && live != nil && runtime.closing == nil
				}
				options.OnRuntimeClosed = func(ctx context.Context, ended LiveSession) error {
					return server.retireSelfClosedRuntime(ctx, id, runtime, ended)
				}
				options.OnBackgroundTaskStopped = func(taskID string, alreadyGone bool) func() {
					return server.claimBackgroundTaskStopped(id, runtime, generation, taskID, alreadyGone)
				}
				originalSDK := options.OnSDK
				options.OnSDK = func(raw json.RawMessage) {
					if server.observeDisconnectSDK(id, runtime, generation, raw) && originalSDK != nil {
						originalSDK(raw)
					}
				}
				live, err = server.options.Factory(runtime.ctx, options, func(raw json.RawMessage) { server.emitDisconnectGeneration(id, runtime, generation, raw) })
			}
		}
		if err == nil && live == nil {
			err = errors.New("shared session factory returned nil")
		}
		if runtime.ctx.Err() != nil && live != nil {
			_ = live.Close(context.Background())
			live = nil
			err = runtime.ctx.Err()
		}
		if err != nil && revokeCollaboration != nil {
			revokeCollaboration()
			revokeCollaboration = nil
		}
		runtime.mu.Lock()
		runtime.revokeCollaboration = revokeCollaboration
		runtime.live = live
		runtime.options = options
		runtime.err = err
		runtime.opening = nil
		close(opening)
		runtime.mu.Unlock()
		if err == nil && options.TeamWorker == nil {
			// MAIN's sessionStarted restoration is owned background work. Its
			// failure is diagnostic and does not invalidate an opened session.
			if done, admitted := server.beginMigrationHTTPAdmission(); admitted {
				go func() {
					defer done()
					if failure := server.TeamLeadStarted(runtime.ctx, id); failure != nil {
						server.options.OnUnhandled(httpapi.UnhandledError{Type: "team_lead_restore_failed", Severity: "error", Summary: "Team lead restoration failed for " + id, Error: failure})
					}
				}()
			}
		}
		server.armDisconnect(id)
		return live, err
	}
}

func (server *Server) forward(id string, socket *connection, raw json.RawMessage) {
	var message protocol.JSONObject
	if message.UnmarshalJSON(raw) == nil && message.String("type") == "stop_generation" {
		server.conversations.Interrupt(id)
	}
	runtime, err := server.runtime(id)
	if err != nil {
		socket.failure(err.Error(), "USER_TURN_FAILED")
		return
	}
	server.forwardRuntime(id, runtime, socket, raw)
}

func (server *Server) forwardRuntime(id string, runtime *runtimeSession, socket *connection, raw json.RawMessage, ingress ...context.Context) {
	var incoming protocol.JSONObject
	_ = incoming.UnmarshalJSON(raw)
	inputContext := runtime.ctx
	if len(ingress) > 0 && ingress[0] != nil {
		inputContext = ingress[0]
	}
	if incoming.String("type") == "user_message" && inputContext.Err() != nil {
		return
	}
	if incoming.String("type") == "stop_generation" {
		runtime.mu.Lock()
		cancelRegisteredUserTurn(runtime)
		runtime.mu.Unlock()
	}
	if handled, err := server.routeTeamPermission(runtime.ctx, id, raw); handled || err != nil {
		if err != nil {
			server.runtimeFailure(id, socket, err)
		}
		return
	}
	if incoming.String("type") == "set_runtime_config" {
		if handled, err := server.selectUnopenedSideChatRuntime(id, runtime, incoming); handled {
			if err != nil {
				var api *settings.APIError
				if errors.As(err, &api) {
					socket.failure(api.Message, api.Code)
				} else {
					server.runtimeFailure(id, socket, err)
				}
			}
			return
		}
	}
	if incoming.String("type") == "user_message" && isSideChatID(id) {
		server.mu.Lock()
		rejected := ""
		if side := server.sideChats[id]; side != nil {
			rejected = side.rejectedRuntime
		}
		server.mu.Unlock()
		if rejected != "" {
			socket.failure(rejected, "RUNTIME_CONFIG_INVALID")
			return
		}
	}
	if incoming.String("type") == "stop_generation" {
		if err := server.PauseTeamWorkers(runtime.ctx, id); err != nil {
			server.runtimeFailure(id, socket, err)
			return
		}
	}
	var reservation *sessioncollaboration.Reservation
	if incoming.String("type") == "user_message" {
		if err := server.collaborationBarrier(inputContext); err != nil {
			if inputContext.Err() == nil {
				server.runtimeFailure(id, socket, err)
			}
			return
		}
		var err error
		reservation, err = server.collaboration.service.AdmitUserTurn(inputContext, id, func() bool { return inputContext.Err() == nil })
		if err == nil {
			err = server.collaboration.service.OnUserInput(inputContext, id, false)
		}
		if err != nil {
			if reservation != nil {
				_ = reservation.Release(context.WithoutCancel(runtime.ctx))
			}
			if inputContext.Err() == nil {
				server.runtimeFailure(id, socket, err)
			}
			return
		}
		defer func() {
			_ = server.collaborationBarrier(runtime.ctx)
			_ = reservation.Release(context.WithoutCancel(runtime.ctx))
		}()
	} else if incoming.String("type") == "stop_generation" {
		if err := server.collaboration.service.OnStopped(runtime.ctx, id); err != nil {
			server.runtimeFailure(id, socket, err)
			return
		}
	}
	live, err := server.liveRuntime(id, runtime)
	// Startup belongs to the shared runtime. A stopped admission can wait for
	// that factory to settle, but must never submit its old message afterward.
	if incoming.String("type") == "user_message" && inputContext.Err() != nil {
		return
	}
	if err == nil && incoming.String("type") == "user_message" && socket != nil {
		err = server.TeamLeadUserMessage(runtime.ctx, id)
	}
	if err == nil {
		if incoming.String("type") == "team_worker_input" {
			worker, ok := live.(nativeTeamWorkerSession)
			if !ok {
				err = unsupported("Team worker actual SDK ingress")
			} else {
				var input protocol.JSONObject
				if e := input.UnmarshalJSON(incoming.Get("input")); e != nil {
					err = e
				} else {
					err = worker.SubmitTeamTask(runtime.ctx, input)
				}
			}
		} else {
			stopTask := incoming.String("type") == "stop_background_task"
			stopAll := incoming.String("type") == "stop_generation"
			taskID := trimJS(incoming.String("taskId"))
			if stopTask || stopAll {
				server.setBackgroundTaskStopRequested(id, runtime, live, taskID, stopAll, true)
			}
			err = live.Handle(inputContext, append(json.RawMessage(nil), raw...))
			if err != nil && (stopTask || stopAll) {
				server.setBackgroundTaskStopRequested(id, runtime, live, taskID, stopAll, false)
			}
		}
	}
	if err == nil && incoming.String("type") == "stop_generation" {
		server.bookendDisconnectTasks(id, runtime, "stopped")
	}
	if err != nil && runtime.ctx.Err() == nil && inputContext.Err() == nil {
		server.runtimeFailure(id, socket, err)
	}
	server.armDisconnect(id)
}

func (server *Server) sendSnapshot(id string, socket *connection, stateOnly bool) {
	server.mu.Lock()
	runtime := server.runtimes[id]
	server.mu.Unlock()
	var live LiveSession
	registered := false
	if runtime != nil {
		runtime.mu.Lock()
		live = runtime.live
		registered = hasRegisteredUserTurn(runtime)
		runtime.mu.Unlock()
	}
	if live == nil {
		state := "idle"
		if registered {
			state = "running"
		}
		if stateOnly {
			_ = socket.object(map[string]any{"type": "session_state", "turnState": state, "activeBackgroundTaskIds": []string{}})
		} else {
			_ = socket.object(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": []string{}, "computerUseRequestIds": []string{}, "turnActive": registered})
		}
		return
	}
	frames, err := live.Snapshot(server.ctx)
	if err == nil {
		frames = server.disconnectSnapshot(runtime, frames)
		if !stateOnly {
			frames, err = server.teamSnapshot(server.ctx, id, frames)
		}
	}
	if err != nil {
		socket.failure(err.Error(), "SESSION_STATE_FAILED")
		return
	}
	for _, raw := range frames {
		var frame protocol.JSONObject
		if frame.UnmarshalJSON(raw) != nil || (frame.String("type") == "session_state") != stateOnly {
			continue
		}
		switch frame.String("type") {
		case "session_state":
			state := json.RawMessage(`"idle"`)
			if registered {
				state = json.RawMessage(`"running"`)
			}
			frame.Set("turnState", state)
		case "permission_requests_snapshot":
			active := json.RawMessage(`false`)
			if registered {
				active = json.RawMessage(`true`)
			}
			frame.Set("turnActive", active)
		}
		raw, _ = frame.MarshalJSON()
		_ = socket.send(raw)
	}
}

func (server *Server) broadcast(id string, raw json.RawMessage) {
	if !json.Valid(raw) {
		return
	}
	server.captureSlashCommandsNotification(id, raw)
	server.mu.Lock()
	var sockets []*connection
	for socket := range server.connections[id] {
		sockets = append(sockets, socket)
	}
	server.mu.Unlock()
	for _, socket := range sockets {
		_ = socket.send(raw)
	}
	var frame struct{ Type, State string }
	_ = json.Unmarshal(raw, &frame)
	if frame.Type == "message_complete" || (frame.Type == "status" && frame.State == "idle") {
		server.armDisconnect(id)
	}
}

func snapshotBusy(ctx context.Context, live LiveSession) bool {
	frames, err := live.Snapshot(ctx)
	if err != nil {
		return true
	}
	for _, raw := range frames {
		var frame struct {
			TurnState               string
			TurnActive              bool
			ActiveBackgroundTaskIDs []string
		}
		if json.Unmarshal(raw, &frame) == nil && (frame.TurnState == "running" || frame.TurnActive || len(frame.ActiveBackgroundTaskIDs) > 0) {
			return true
		}
	}
	return false
}

func (server *Server) armDisconnect(id string) {
	server.mu.Lock()
	runtime := server.runtimes[id]
	connected := len(server.connections[id]) > 0
	closed := server.closed || server.migration.active
	server.mu.Unlock()
	if runtime == nil {
		return
	}
	if connected || closed {
		server.cancelDisconnect(runtime)
		return
	}
	runtime.mu.Lock()
	live, opening := runtime.live, runtime.opening != nil
	retired, worker := runtime.retired || runtime.closing != nil, runtime.options.TeamWorker != nil
	pending := runtime.admitting || len(runtime.admission) > 0
	runtime.mu.Unlock()
	if retired || worker || live == nil && !opening {
		return
	}
	if live != nil && server.closeLostDisconnectRuntime(id, runtime, live) {
		return
	}
	opening = opening || disconnectRuntimeTransition(live) != nil
	busy := false
	if live != nil {
		busy = snapshotBusy(runtime.ctx, live)
	}
	team := server.TeamHasPendingWork(id)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.retired || runtime.live != live || runtime.closing != nil {
		return
	}
	d := runtime.disconnectState()
	kind := d.decision(pending, busy, team, opening)
	if kind == "" {
		d.cancelTimer()
		return
	}
	// Progress must not restart the 31-minute clock. An expired startup
	// decision likewise remains owned until its factory settles.
	if d.kind == kind && (d.timer != nil || d.startupExpired) {
		return
	}
	d.cancelTimer()
	d.kind = kind
	delay := 30 * time.Second
	if server.grace != nil {
		delay = time.Duration(server.grace.Milliseconds()) * time.Millisecond
	}
	if kind != "idle" {
		delay = disconnectedWorkCeiling
	}
	runtime.epoch++
	epoch, sequence := runtime.epoch, d.sequence
	d.timer = server.afterRuntimeTimer(delay, func() { server.expireDisconnectDecision(id, runtime, epoch, sequence, kind) })
}

// Kept as the existing idle-timer test seam. Production callbacks also capture
// the per-decision sequence and kind, so canceled callbacks cannot claim a
// replacement timer from the same runtime.
func (server *Server) expireDisconnect(id string, runtime *runtimeSession, epoch uint64) {
	runtime.mu.Lock()
	d := runtime.disconnectState()
	sequence, kind := d.sequence, d.kind
	runtime.mu.Unlock()
	server.expireDisconnectDecision(id, runtime, epoch, sequence, kind)
}
func (server *Server) expireDisconnectDecision(id string, runtime *runtimeSession, epoch, sequence uint64, kind string) {
	server.mu.Lock()
	runtime.mu.Lock()
	d := runtime.disconnectState()
	eligible := !server.closed && !server.migration.active && len(server.connections[id]) == 0 && server.runtimes[id] == runtime && !runtime.retired && runtime.closing == nil && runtime.epoch == epoch && d.sequence == sequence && d.kind == kind
	live, opening := runtime.live, runtime.opening
	if eligible && opening != nil {
		d.timer = nil
		d.startupExpired = true
	}
	runtime.mu.Unlock()
	server.mu.Unlock()
	if !eligible {
		return
	}
	if opening == nil {
		if pending := disconnectRuntimeTransition(live); pending != nil {
			// This channel belongs to the internal Host replacement, rather than
			// the outer desktop factory. Keep the same cleanup-decision ticket.
			runtime.mu.Lock()
			if d.sequence == sequence {
				d.timer = nil
				d.startupExpired = true
			}
			runtime.mu.Unlock()
			go func() {
				select {
				case <-pending:
					server.expireDisconnectDecision(id, runtime, epoch, sequence, kind)
				case <-runtime.ctx.Done():
				}
			}()
			return
		}
	}
	if opening != nil {
		// The factory is owned by runtime.ctx; Close joins it. This wait cannot
		// promote a superseded timeout after reconnect or a new user turn.
		go func() {
			select {
			case <-opening:
				server.expireDisconnectDecision(id, runtime, epoch, sequence, kind)
			case <-runtime.ctx.Done():
			}
		}()
		return
	}
	if live == nil {
		return
	}
	busy, team := snapshotBusy(runtime.ctx, live), server.TeamHasPendingWork(id)
	runtime.mu.Lock()
	pending := runtime.admitting || len(runtime.admission) > 0
	current := d.decision(pending, busy, team, false)
	runtime.mu.Unlock()
	if current != kind {
		server.armDisconnect(id)
		return
	}
	server.mu.Lock()
	runtime.mu.Lock()
	if server.closed || server.migration.active || len(server.connections[id]) > 0 || server.runtimes[id] != runtime || runtime.retired || runtime.epoch != epoch || d.sequence != sequence || d.kind != kind || runtime.live != live || runtime.closing != nil || d.decision(runtime.admitting || len(runtime.admission) > 0, busy, team, false) != kind {
		runtime.mu.Unlock()
		server.mu.Unlock()
		return
	}
	runtime.live = nil
	runtime.slashCommands = nil
	if runtime.revokeCollaboration != nil {
		runtime.revokeCollaboration()
		runtime.revokeCollaboration = nil
	}
	d.cancelTimer()
	d.cliRunning, d.rootWaiting, d.rootActive = false, false, false
	clear(d.tool)
	clear(d.computer)
	runtime.epoch++
	closing := make(chan struct{})
	runtime.closing = closing
	runtime.mu.Unlock()
	server.mu.Unlock()
	// Close reaps and joins the same native task group. Bookends follow its
	// successful completion, so no UI terminal status claims a live process
	// has stopped before the native cancellation has been acknowledged.
	err := live.Close(runtime.ctx)
	if err == nil {
		server.bookendDisconnectTasks(id, runtime, "runtime_exit")
	}
	runtime.mu.Lock()
	runtime.closeError = err
	runtime.closing = nil
	close(closing)
	runtime.mu.Unlock()
}

func (server *Server) sessionOptions(info sessionInfo) SessionOptions {
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(info.Metadata, &metadata)
	workDir := stringField(metadata, "workDir")
	if workDir == "" {
		workDir = info.WorkDir
	}
	count := info.MessageCount
	_ = json.Unmarshal(metadata["transcriptMessageCount"], &count)
	options := SessionOptions{ID: info.ID, WorkDir: workDir, PermissionMode: stringField(metadata, "permissionMode"), Resume: count > 0, Metadata: info.Metadata, OnCanonicalTranscriptMutation: func(path string) error { server.noteSessionMutation(path); return nil }, UpdateLaunchMetadata: func(ctx context.Context, raw json.RawMessage) error {
		return server.updateLaunchMetadata(ctx, info.ID, raw)
	}}
	options.SideChat = server.sideChatOptions(info.ID)
	if raw := metadata["repository"]; len(raw) > 0 && string(raw) != "null" {
		options.PrepareRepositoryLaunch = func(ctx context.Context) (RepositorySessionLaunch, error) {
			return server.prepareRepositorySessionLaunch(ctx, info, workDir, count > 0)
		}
		options.FinalizeRepositoryLaunch = func(ctx context.Context, cwd string, repository json.RawMessage) error {
			return server.finalizeRepositorySessionLaunch(ctx, info, cwd, repository)
		}
	}
	options.ResolveSessionReferences = func(ctx context.Context, content, references json.RawMessage) (json.RawMessage, error) {
		return sessionreference.Resolve(ctx, content, references, func(ctx context.Context, id json.RawMessage) (bool, error) {
			var name string
			_ = json.Unmarshal(id, &name)
			_, e := server.findSession(ctx, name)
			var missing *apiError
			if errors.As(e, &missing) && missing.status == 404 {
				return false, nil
			}
			return e == nil, e
		})
	}
	return options
}

func (socket *connection) closePetSwitched() { socket.closeWithReason(1000, "Pet session switched") }

func (socket *connection) closeWithReason(code uint16, reason string) {
	socket.mu.Lock()
	payload := make([]byte, 2, len(reason)+2)
	binary.BigEndian.PutUint16(payload, code)
	payload = append(payload, []byte(reason)...)
	codec := websocket.Codec{Marshal: func(any) ([]byte, byte, error) { return payload, websocket.CloseFrame, nil }}
	_ = codec.Send(socket.ws, nil)
	_ = socket.ws.Close()
	socket.mu.Unlock()
}
