package imregistration

import (
	"context"
	"errors"
	"fmt"
	"go.mau.fi/whatsmeow/socket"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/types/events"
)

// This is the original protocol's createSocket dependency, not another chat
// runtime. Production always adapts the actual pinned Node-compatible client.
type WhatsAppLoginSocket interface {
	Connect(context.Context) error
	Close(context.Context) error
	WaitCredsSave(context.Context) error
	AccountJID() string
}
type WhatsAppLoginOptions struct {
	ClientOptions wa.NodeWhatsAppOptions
	Now           func() time.Time
	UUID          func() string
	Warn          func(error)
	CreateSocket  func(context.Context, wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error)
	Schedule      func(time.Duration, func()) func()
}
type whatsAppNodeLoginSocket struct {
	*wa.NodeWhatsAppClient
	onConnectionError func(error)
}

func (n whatsAppNodeLoginSocket) Connect(ctx context.Context) error {
	return n.NodeWhatsAppClient.StartLoginConnection(ctx, n.onConnectionError)
}

// The original websocket caller maps network errors to 408 and HTTP rejection
// statuses >=400 verbatim. Noise/protocol errors have no restart code.
func whatsAppLoginDialStatus(err error) int {
	var status socket.ErrWithStatusCode
	if errors.As(err, &status) {
		if status.StatusCode >= 400 {
			return status.StatusCode
		}
		return 500
	}
	var network net.Error
	if errors.As(err, &network) {
		return 408
	}
	return 500
}

func (n whatsAppNodeLoginSocket) WaitCredsSave(ctx context.Context) error {
	return n.Store.WaitCredsSave(ctx)
}
func (n whatsAppNodeLoginSocket) AccountJID() string {
	if n.Client.Store.ID != nil {
		return n.Client.Store.ID.String()
	}
	return ""
}

type whatsAppLogin struct {
	ctx                                   context.Context
	cancel                                context.CancelFunc
	stopParent                            func() bool
	key, authDir, qr, accountJID, failure string
	created                               time.Time
	client                                WhatsAppLoginSocket
	connected                             bool
	restarts                              int
	qrGeneration                          uint64
	stopQR                                func()
}
type WhatsAppLoginService struct {
	mu        sync.Mutex
	o         WhatsAppLoginOptions
	ctx       context.Context
	cancel    context.CancelFunc
	sessions  map[string]*whatsAppLogin
	closed    bool
	wg        sync.WaitGroup
	closeDone chan struct{}
}

func NewWhatsAppLogin(o WhatsAppLoginOptions) *WhatsAppLoginService {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UUID == nil {
		o.UUID = uuid.NewString
	}
	if o.Warn == nil {
		o.Warn = func(e error) { fmt.Fprintln(os.Stderr, "[WhatsApp] Login socket restart failed:", e) }
	}
	if o.Schedule == nil {
		o.Schedule = func(delay time.Duration, f func()) func() {
			timer := time.AfterFunc(delay, f)
			return func() { timer.Stop() }
		}
	}
	if o.CreateSocket == nil {
		o.CreateSocket = func(ctx context.Context, options wa.NodeWhatsAppOptions) (WhatsAppLoginSocket, error) {
			client, e := wa.NewNodeWhatsAppClient(ctx, options)
			if e != nil {
				return nil, e
			}
			return &whatsAppNodeLoginSocket{NodeWhatsAppClient: client, onConnectionError: func(err error) {
				if options.OnEvent == nil {
					return
				}
				status := whatsAppLoginDialStatus(err)
				if status == 401 {
					options.OnEvent(&events.LoggedOut{})
					return
				}
				options.OnEvent(&events.StreamError{Code: strconv.Itoa(status)})
			}}, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WhatsAppLoginService{o: o, ctx: ctx, cancel: cancel, sessions: map[string]*whatsAppLogin{}, closeDone: make(chan struct{})}
}

// Admission and Close share the same mutex: an accepted filesystem/socket
// operation remains owned until it completes, including after map removal.
func (s *WhatsAppLoginService) beginOperation() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("WhatsApp login service closed")
	}
	s.wg.Add(1)
	return nil
}
func (s *WhatsAppLoginService) launch(f func()) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() { defer s.wg.Done(); f() }()
	return true
}
func (s *WhatsAppLoginService) stopSessionLocked(session *whatsAppLogin) {
	session.cancel()
	if session.stopParent != nil {
		session.stopParent()
	}
	if session.stopQR != nil {
		session.stopQR()
		session.stopQR = nil
	}
	session.qrGeneration++
}
func (s *WhatsAppLoginService) closeClients(clients []WhatsAppLoginSocket) {
	for _, client := range clients {
		if client != nil {
			_ = client.Close(context.Background())
		}
	}
}
func (s *WhatsAppLoginService) expire() {
	s.mu.Lock()
	var clients []WhatsAppLoginSocket
	for key, session := range s.sessions {
		if s.o.Now().Sub(session.created) <= 2*time.Minute {
			continue
		}
		delete(s.sessions, key)
		s.stopSessionLocked(session)
		clients = append(clients, session.client)
	}
	s.mu.Unlock()
	s.closeClients(clients)
}
func (s *WhatsAppLoginService) connect(session *whatsAppLogin) error {
	s.mu.Lock()
	valid := !s.closed && s.sessions[session.key] == session && session.ctx.Err() == nil
	s.mu.Unlock()
	if !valid {
		return context.Canceled
	}
	options := s.o.ClientOptions
	options.AuthDir = session.authDir
	options.DisableAutoReconnect = true
	prior := options.OnEvent
	var client WhatsAppLoginSocket
	options.OnEvent = func(value any) {
		s.mu.Lock()
		current := !s.closed && s.sessions[session.key] == session && session.client == client
		s.mu.Unlock()
		if !current {
			return
		}
		switch event := value.(type) {
		case *events.QR:
			s.rotateQR(session, client, append([]string(nil), event.Codes...))
		case *events.Connected:
			s.launch(func() {
				if e := client.WaitCredsSave(session.ctx); e != nil {
					return
				}
				s.mu.Lock()
				if !s.closed && s.sessions[session.key] == session && session.client == client {
					session.connected = true
					session.accountJID = client.AccountJID()
					if session.stopQR != nil {
						session.stopQR()
						session.stopQR = nil
					}
					session.qrGeneration++
				}
				s.mu.Unlock()
			})
		case *events.LoggedOut:
			s.mu.Lock()
			session.failure = "WhatsApp session logged out. Please scan again."
			s.mu.Unlock()
		case *events.ManualLoginReconnect:
			s.restart(session, client)
		case *events.StreamError:
			if event.Code == "408" {
				s.restart(session, client)
			} else {
				s.fail(session, client)
			}
		case *events.Disconnected, *events.ConnectFailure, *events.TemporaryBan, *events.StreamReplaced:
			s.fail(session, client)
		}
		if prior != nil {
			prior(value)
		}
	}
	var e error
	client, e = s.o.CreateSocket(session.ctx, options)
	if e != nil {
		return e
	}
	s.mu.Lock()
	if s.closed || s.sessions[session.key] != session || session.ctx.Err() != nil {
		s.mu.Unlock()
		_ = client.Close(context.Background())
		return context.Canceled
	}
	session.client = client
	s.mu.Unlock()
	return client.Connect(session.ctx)
}
func (s *WhatsAppLoginService) rotateQR(session *whatsAppLogin, client WhatsAppLoginSocket, codes []string) {
	s.mu.Lock()
	if len(codes) == 0 || s.closed || s.sessions[session.key] != session || session.client != client || session.connected {
		s.mu.Unlock()
		return
	}
	session.qrGeneration++
	generation := session.qrGeneration
	if session.stopQR != nil {
		session.stopQR()
	}
	session.qr = codes[0]
	s.mu.Unlock()
	var next func(int)
	next = func(index int) {
		delay := 20 * time.Second
		if index == 0 {
			delay = 60 * time.Second
		}
		cancel := s.o.Schedule(delay, func() {
			s.mu.Lock()
			valid := !s.closed && s.sessions[session.key] == session && session.client == client && session.qrGeneration == generation && !session.connected
			if valid && index+1 < len(codes) {
				session.qr = codes[index+1]
			}
			s.mu.Unlock()
			if !valid {
				return
			}
			if index+1 < len(codes) {
				next(index + 1)
			} else {
				s.restart(session, client)
			}
		})
		s.mu.Lock()
		if !s.closed && s.sessions[session.key] == session && session.client == client && session.qrGeneration == generation {
			session.stopQR = cancel
			s.mu.Unlock()
		} else {
			s.mu.Unlock()
			cancel()
		}
	}
	next(0)
}
func (s *WhatsAppLoginService) fail(session *whatsAppLogin, client WhatsAppLoginSocket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[session.key] == session && session.client == client && !session.connected {
		session.failure = "WhatsApp login connection closed. Please retry."
	}
}
func (s *WhatsAppLoginService) restart(session *whatsAppLogin, client WhatsAppLoginSocket) {
	s.mu.Lock()
	if s.closed || s.sessions[session.key] != session || session.client != client || session.connected {
		s.mu.Unlock()
		return
	}
	if session.restarts >= 5 {
		s.mu.Unlock()
		s.fail(session, client)
		return
	}
	session.restarts++
	// Reserve ownership before unpublishing the old socket. Close may run
	// immediately after unlock and must still join this socket cleanup.
	s.wg.Add(1)
	session.client = nil
	if session.stopQR != nil {
		session.stopQR()
		session.stopQR = nil
	}
	session.qrGeneration++
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		_ = client.Close(context.Background())
		e := client.WaitCredsSave(session.ctx)
		if e == nil {
			e = s.connect(session)
			if e == nil {
				return
			}
		}
		s.mu.Lock()
		active := !s.closed && s.sessions[session.key] == session
		if active {
			session.failure = "WhatsApp login connection closed. Please retry."
		}
		s.mu.Unlock()
		if active {
			s.o.Warn(e)
		}
	}()
}

// A session survives renderer response/disconnect. The parent is the server
// lifetime, ensuring an accepted Begin cannot await Close behind the API join.
func (s *WhatsAppLoginService) Start(ctx context.Context, authDir string) (protocol.JSONObject, error) {
	if e := s.beginOperation(); e != nil {
		return protocol.JSONObject{}, e
	}
	defer s.wg.Done()
	s.expire()
	abs, e := filepath.Abs(authDir)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	lifetime, cancel := context.WithCancel(s.ctx)
	parentStop := context.AfterFunc(ctx, cancel)
	session := &whatsAppLogin{key: s.o.UUID(), authDir: abs, created: s.o.Now(), ctx: lifetime, cancel: cancel, stopParent: parentStop}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		parentStop()
		return protocol.JSONObject{}, errors.New("WhatsApp login service closed")
	}
	s.sessions[session.key] = session
	s.mu.Unlock()
	started := s.o.Now() // Source creates its waitForQr promise before connect.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	if e = s.connect(session); e != nil {
		s.mu.Lock()
		delete(s.sessions, session.key)
		s.stopSessionLocked(session)
		s.mu.Unlock()
		s.closeClients([]WhatsAppLoginSocket{session.client})
		return protocol.JSONObject{}, e
	}
	for {
		select {
		case <-ticker.C:
		case <-session.ctx.Done():
			return protocol.JSONObject{}, context.Cause(session.ctx)
		}
		s.mu.Lock()
		qr, connected := session.qr, session.connected
		s.mu.Unlock()
		if qr != "" || connected || s.o.Now().Sub(started) > 20*time.Second {
			result := record(field("sessionKey", session.key))
			message := "Waiting for WhatsApp QR code..."
			if qr != "" {
				result.Set("qr", encoded(qr))
				message = "Scan this QR in WhatsApp > Linked devices."
			}
			result.Set("message", encoded(message))
			return result, nil
		}
	}
}
func (s *WhatsAppLoginService) Poll(key string) (protocol.JSONObject, error) {
	if e := s.beginOperation(); e != nil {
		return protocol.JSONObject{}, e
	}
	defer s.wg.Done()
	s.expire()
	s.mu.Lock()
	session := s.sessions[key]
	if session == nil {
		s.mu.Unlock()
		return whatsAppExpired(), nil
	}
	connected, failure, qr, client, jid, dir := session.connected, session.failure, session.qr, session.client, session.accountJID, session.authDir
	s.mu.Unlock()
	if connected {
		// Original deletion occurs after the save barrier. Concurrent callers that
		// already observed connected are not turned into false expiry while waiting.
		// Source awaits an uncancelled save promise. Another successful poll
		// may close the socket, but cannot reject this already accepted wait.
		// The operation ref and native client Close still join the physical save.
		if e := client.WaitCredsSave(context.Background()); e != nil {
			return protocol.JSONObject{}, e
		}
		if jid == "" {
			jid = client.AccountJID()
		}
		s.mu.Lock()
		delete(s.sessions, key)
		s.stopSessionLocked(session)
		s.mu.Unlock()
		s.closeClients([]WhatsAppLoginSocket{client})
		return record(field("connected", true), field("accountJid", jid), field("authDir", dir), field("message", "WhatsApp linked successfully.")), nil
	}
	if failure != "" {
		s.mu.Lock()
		delete(s.sessions, key)
		s.stopSessionLocked(session)
		s.mu.Unlock()
		s.closeClients([]WhatsAppLoginSocket{client})
		return record(field("connected", false), field("status", "error"), field("message", failure)), nil
	}
	result := record(field("connected", false), field("status", "waiting"))
	message := "Waiting for WhatsApp QR code..."
	if qr != "" {
		result.Set("qr", encoded(qr))
		message = "Waiting for WhatsApp scan confirmation..."
	}
	result.Set("message", encoded(message))
	return result, nil
}
func whatsAppExpired() protocol.JSONObject {
	return record(field("connected", false), field("status", "expired"), field("message", "WhatsApp login session expired. Generate a new QR code."))
}
func (s *WhatsAppLoginService) Logout(authDir string) error {
	if e := s.beginOperation(); e != nil {
		return e
	}
	defer s.wg.Done()
	abs, e := filepath.Abs(authDir)
	if e != nil {
		return e
	}
	s.mu.Lock()
	var clients []WhatsAppLoginSocket
	for key, session := range s.sessions {
		if session.authDir == abs {
			delete(s.sessions, key)
			s.stopSessionLocked(session)
			clients = append(clients, session.client)
		}
	}
	s.mu.Unlock()
	s.closeClients(clients)
	return os.RemoveAll(abs)
}
func (s *WhatsAppLoginService) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
		var clients []WhatsAppLoginSocket
		for key, session := range s.sessions {
			delete(s.sessions, key)
			s.stopSessionLocked(session)
			clients = append(clients, session.client)
		}
		go func() { s.closeClients(clients); s.wg.Wait(); close(s.closeDone) }()
	}
	done := s.closeDone
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
