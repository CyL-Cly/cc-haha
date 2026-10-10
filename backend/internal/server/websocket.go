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
	socket *connection
	raw    json.RawMessage
	ctx    context.Context
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
	if server.closed || server.deleting[id] {
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
	server.sendSnapshot(id, socket)
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
			server.sendSnapshot(id, socket)
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
	if server.deleting[id] {
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
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed || server.deleting[id] {
		cancel()
		return nil, context.Canceled
	}
	if existing := server.runtimes[id]; existing != nil {
		cancel()
		return existing, nil
	}
	server.runtimes[id] = value
	go server.admit(id, value)
	return value, nil
}

// The socket reader records user frames synchronously in arrival order. One
// worker admits them to the shared session; controls bypass this queue so an
// active turn can still receive permission replies and cancellation.
func (server *Server) enqueue(id string, socket *connection, raw json.RawMessage) {
	var message protocol.JSONObject
	if message.UnmarshalJSON(raw) == nil && message.String("type") == "user_message" {
		server.conversations.Begin(id)
	}
	runtime, err := server.runtime(id)
	if err != nil {
		socket.failure(err.Error(), "USER_TURN_FAILED")
		return
	}
	runtime.mu.Lock()
	if runtime.retired || runtime.ctx.Err() != nil {
		runtime.mu.Unlock()
		return
	}
	inputContext := runtime.ctx
	if input, ok := runtime.live.(PlanRuntimeApprovalSession); ok && message.String("type") == "user_message" {
		inputContext = input.CaptureRuntimeInput(inputContext)
	}
	runtime.admission = append(runtime.admission, admissionFrame{socket: socket, raw: append(json.RawMessage(nil), raw...), ctx: inputContext})
	runtime.epoch++
	if runtime.timer != nil {
		runtime.timer.Stop()
		runtime.timer = nil
	}
	runtime.mu.Unlock()
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
		runtime.mu.Unlock()
		server.forwardRuntime(id, runtime, frame.socket, frame.raw, frame.ctx)
		runtime.mu.Lock()
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
					owned := !server.closed && server.runtimes[id] == runtime && len(server.connections[id]) > 0
					server.mu.Unlock()
					if !owned {
						return false
					}
					runtime.mu.Lock()
					defer runtime.mu.Unlock()
					return !runtime.retired && runtime.ctx.Err() == nil && runtime.live == live && live != nil && runtime.closing == nil
				}
				live, err = server.options.Factory(runtime.ctx, options, func(raw json.RawMessage) { server.emitTeamWorker(id, raw) })
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
	if handled, err := server.routeTeamPermission(runtime.ctx, id, raw); handled || err != nil {
		if err != nil {
			server.runtimeFailure(id, socket, err)
		}
		return
	}
	var incoming protocol.JSONObject
	_ = incoming.UnmarshalJSON(raw)
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
		if err := server.StopTeamWorkers(runtime.ctx, id); err != nil {
			server.runtimeFailure(id, socket, err)
			return
		}
	}
	var reservation *sessioncollaboration.Reservation
	if incoming.String("type") == "user_message" {
		if err := server.collaborationBarrier(runtime.ctx); err != nil {
			server.runtimeFailure(id, socket, err)
			return
		}
		var err error
		reservation, err = server.collaboration.service.AdmitUserTurn(runtime.ctx, id, func() bool { return runtime.ctx.Err() == nil })
		if err == nil {
			err = server.collaboration.service.OnUserInput(runtime.ctx, id, false)
		}
		if err != nil {
			if reservation != nil {
				_ = reservation.Release(context.WithoutCancel(runtime.ctx))
			}
			server.runtimeFailure(id, socket, err)
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
			ctx := runtime.ctx
			if len(ingress) > 0 && ingress[0] != nil {
				ctx = ingress[0]
			}
			err = live.Handle(ctx, append(json.RawMessage(nil), raw...))
		}
	}
	if err != nil && runtime.ctx.Err() == nil {
		server.runtimeFailure(id, socket, err)
	}
}

func (server *Server) sendSnapshot(id string, socket *connection) {
	server.mu.Lock()
	runtime := server.runtimes[id]
	server.mu.Unlock()
	var live LiveSession
	if runtime != nil {
		runtime.mu.Lock()
		live = runtime.live
		runtime.mu.Unlock()
	}
	if live == nil {
		_ = socket.object(map[string]any{"type": "session_state", "turnState": "idle", "activeBackgroundTaskIds": []string{}})
		_ = socket.object(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": []string{}, "computerUseRequestIds": []string{}, "turnActive": false})
		return
	}
	frames, err := live.Snapshot(server.ctx)
	if err == nil {
		frames, err = server.teamSnapshot(server.ctx, id, frames)
	}
	if err != nil {
		socket.failure(err.Error(), "SESSION_STATE_FAILED")
		return
	}
	for _, raw := range frames {
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
	closed := server.closed
	server.mu.Unlock()
	if connected || closed || runtime == nil {
		return
	}
	runtime.mu.Lock()
	live := runtime.live
	retired := runtime.retired
	worker := runtime.options.TeamWorker != nil
	pending := runtime.admitting || len(runtime.admission) > 0
	runtime.mu.Unlock()
	if live == nil || retired || pending || worker || snapshotBusy(runtime.ctx, live) {
		return
	}
	delay := 30 * time.Second
	if server.grace != nil {
		delay = time.Duration(server.grace.Milliseconds()) * time.Millisecond
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.retired || runtime.live != live {
		return
	}
	runtime.epoch++
	epoch := runtime.epoch
	if runtime.timer != nil {
		runtime.timer.Stop()
	}
	runtime.timer = time.AfterFunc(delay, func() { server.expireDisconnect(id, runtime, epoch) })
}

func (server *Server) expireDisconnect(id string, runtime *runtimeSession, epoch uint64) {
	runtime.mu.Lock()
	live := runtime.live
	eligible := runtime.epoch == epoch && !runtime.retired && runtime.closing == nil && !runtime.admitting && len(runtime.admission) == 0
	runtime.mu.Unlock()
	if live == nil || !eligible || snapshotBusy(runtime.ctx, live) {
		return
	}
	server.mu.Lock()
	runtime.mu.Lock()
	if server.closed || len(server.connections[id]) > 0 || runtime.retired || runtime.epoch != epoch || runtime.live != live || runtime.closing != nil || runtime.admitting || len(runtime.admission) > 0 {
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
	runtime.timer = nil
	runtime.epoch++
	closing := make(chan struct{})
	runtime.closing = closing
	runtime.mu.Unlock()
	server.mu.Unlock()
	_ = live.Close(runtime.ctx)
	runtime.mu.Lock()
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
