package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/conversationapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

type sdkPeer struct {
	socket  *connection
	capture *conversationapi.SDKCapture
}
type sdkState struct {
	mu       sync.Mutex
	peers    map[string]*sdkPeer
	all      map[string]map[*sdkPeer]struct{}
	captures map[string]*conversationapi.SDKCapture
	closed   bool
	wg       sync.WaitGroup
}
type SDKOutputSession interface {
	AcceptSDKOutput(context.Context, json.RawMessage) error
	BindSDKResponseTransport(func(json.RawMessage) error) func()
}

func (s *Server) newSDKState() {
	s.sdk = &sdkState{peers: map[string]*sdkPeer{}, all: map[string]map[*sdkPeer]struct{}{}, captures: map[string]*conversationapi.SDKCapture{}}
}
func (s *Server) sdkCapture(id string) *conversationapi.SDKCapture {
	state := s.sdk
	state.mu.Lock()
	defer state.mu.Unlock()
	capture := state.captures[id]
	if capture == nil {
		capture = conversationapi.NewSDKCapture()
		state.captures[id] = capture
	}
	return capture
}
func (s *Server) observeSDKCapture(id string, data json.RawMessage) {
	s.sdk.mu.Lock()
	defer s.sdk.mu.Unlock()
	if s.sdk.closed {
		return
	}
	capture := s.sdk.captures[id]
	if capture == nil {
		capture = conversationapi.NewSDKCapture()
		s.sdk.captures[id] = capture
	}
	capture.Observe(data)
}
func closeSDKSocket(ws *websocket.Conn, code uint16, reason string) {
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data, code)
	data = append(data, []byte(reason)...)
	ws.PayloadType = websocket.CloseFrame
	_, _ = ws.Write(data)
	_ = ws.Close()
}
func (s *Server) sdkWebsocket(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if !sessionIDPattern.MatchString(id) {
		http.Error(w, "Invalid session ID", 400)
		return
	}
	token := r.URL.Query().Get("token")
	websocket.Server{Handshake: func(_ *websocket.Config, _ *http.Request) error { return nil }, Handler: func(ws *websocket.Conn) {
		s.sdk.mu.Lock()
		if s.sdk.closed {
			s.sdk.mu.Unlock()
			_ = ws.Close()
			return
		}
		s.sdk.wg.Add(1)
		s.sdk.mu.Unlock()
		defer s.sdk.wg.Done()
		if !s.authorizeSessionCredential(id, token) {
			closeSDKSocket(ws, 1008, "Invalid SDK token")
			return
		}
		socket := &connection{ws: ws}
		peer := &sdkPeer{socket: socket, capture: s.sdkCapture(id)}
		s.mu.Lock()
		runtime := s.runtimes[id]
		s.mu.Unlock()
		if runtime == nil {
			closeSDKSocket(ws, 1008, "Invalid SDK token")
			return
		}
		runtime.mu.Lock()
		live := runtime.live
		runtime.mu.Unlock()
		ingress, ok := live.(SDKOutputSession)
		if !ok {
			socket.failure("Go server capability is not implemented: SDK output consumer", "GO_MIGRATION_NOT_READY")
			_ = ws.Close()
			return
		}
		s.sdk.mu.Lock()
		if s.sdk.closed {
			s.sdk.mu.Unlock()
			_ = ws.Close()
			return
		}
		s.sdk.peers[id] = peer
		if s.sdk.all[id] == nil {
			s.sdk.all[id] = map[*sdkPeer]struct{}{}
		}
		s.sdk.all[id][peer] = struct{}{}
		s.sdk.mu.Unlock()
		defer func() {
			s.sdk.mu.Lock()
			if s.sdk.peers[id] == peer {
				delete(s.sdk.peers, id)
			}
			delete(s.sdk.all[id], peer)
			s.sdk.mu.Unlock()
			_ = ws.Close()
		}()
		unbind := ingress.BindSDKResponseTransport(func(data json.RawMessage) error {
			return socket.send(append(append(json.RawMessage(nil), data...), '\n'))
		})
		defer unbind()
		for {
			var data string
			if e := websocket.Message.Receive(ws, &data); e != nil {
				return
			}
			if !s.authorizeSessionCredential(id, token) {
				closeSDKSocket(ws, 1008, "Stale SDK token")
				return
			}
			finishPayload := func() {}
			if batch, ok := ingress.(interface{ BeginSDKPayload() func() }); ok {
				finishPayload = batch.BeginSDKPayload()
			}
			for _, message := range conversationapi.DecodeSDKPayload(data) {
				var value protocol.JSONObject
				_ = value.UnmarshalJSON(message)
				late := false
				if value.String("type") == "control_request" && sdkRequestSubtype(value) == "can_use_tool" {
					if gate, ok := ingress.(interface{ AcceptsSDKPermissionRequests() bool }); ok {
						late = !gate.AcceptsSDKPermissionRequests()
					}
				}
				accepted := false
				if late {
					accepted = peer.capture.Remember(message)
				} else {
					accepted = peer.capture.Observe(message)
				}
				if !accepted {
					continue
				}
				if e := ingress.AcceptSDKOutput(runtime.ctx, message); e != nil {
					socket.failure(e.Error(), "GO_MIGRATION_NOT_READY")
				}
			}
			finishPayload()
		}
	}}.ServeHTTP(w, r)
}

func sdkRequestSubtype(value protocol.JSONObject) string {
	var request protocol.JSONObject
	_ = request.UnmarshalJSON(value.Get("request"))
	return request.String("subtype")
}

// Reconnecting keeps capture state. A new owned runtime resets it, just as the
// original SessionProcess constructor creates new replay and retention state.
func (s *Server) resetSDKSession(id string) {
	s.revokeSDKSession(id)
	s.sdk.mu.Lock()
	s.sdk.captures[id] = conversationapi.NewSDKCapture()
	s.sdk.mu.Unlock()
}
func (s *Server) revokeSDKSession(id string) {
	s.sdk.mu.Lock()
	sockets := []*sdkPeer{}
	for peer := range s.sdk.all[id] {
		sockets = append(sockets, peer)
	}
	delete(s.sdk.captures, id)
	s.sdk.mu.Unlock()
	for _, peer := range sockets {
		_ = peer.socket.ws.Close()
	}
}
func (s *Server) closeSDK(ctx context.Context) error {
	s.sdk.mu.Lock()
	s.sdk.closed = true
	sockets := []*sdkPeer{}
	for _, group := range s.sdk.all {
		for peer := range group {
			sockets = append(sockets, peer)
		}
	}
	clear(s.sdk.captures)
	s.sdk.mu.Unlock()
	for _, peer := range sockets {
		_ = peer.socket.ws.Close()
	}
	done := make(chan struct{})
	go func() { s.sdk.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
