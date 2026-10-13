package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type BridgeHandler func(context.Context, protocol.JSONObject) error
type bridgeSession struct {
	lastActivity           time.Time
	turnActive, cliRunning bool
	pending                map[string]bool
	ordinal                uint64
	id                     string
	socket                 *Socket
	cancel                 context.CancelFunc
	open                   chan struct{}
	initial                chan struct{}
	done                   chan struct{}
	handler                BridgeHandler
	attempts               int
	ended                  bool
}
type Bridge struct {
	Now         func() time.Time
	idleRelease time.Duration
	sequence    uint64
	config      Config
	tls         *tls.Config
	mu          sync.Mutex
	sessions    map[string]*bridgeSession
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	Log         func(string)
}

type BridgeOptions struct {
	Now                            func() time.Time
	IdleRelease, HeartbeatInterval time.Duration
}

const IdleSessionRelease = 30 * time.Minute

func NewBridge(ctx context.Context, config Config, tlsConfig *tls.Config, log func(string), options ...BridgeOptions) *Bridge {
	o := BridgeOptions{Now: time.Now, IdleRelease: IdleSessionRelease, HeartbeatInterval: 30 * time.Second}
	if len(options) > 0 {
		if options[0].Now != nil {
			o.Now = options[0].Now
		}
		if options[0].IdleRelease > 0 {
			o.IdleRelease = options[0].IdleRelease
		}
		if options[0].HeartbeatInterval > 0 {
			o.HeartbeatInterval = options[0].HeartbeatInterval
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	b := &Bridge{config: config, tls: tlsConfig, sessions: map[string]*bridgeSession{}, ctx: ctx, cancel: cancel, Log: log, Now: o.Now, idleRelease: o.IdleRelease}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		timer := time.NewTicker(o.HeartbeatInterval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				b.ReleaseIdleSessions()
				b.mu.Lock()
				chats := []string{}
				for chat := range b.sessions {
					chats = append(chats, chat)
				}
				b.mu.Unlock()
				for _, chat := range chats {
					_ = b.Send(chat, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("ping")}))
				}
			}
		}
	}()
	return b
}
func (b *Bridge) SessionID(chat string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.sessions[chat]; s != nil {
		return s.id
	}
	return ""
}
func (b *Bridge) IsOpen(chat, id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sessions[chat]
	return s != nil && s.socket != nil && !s.ended && (id == "" || s.id == id)
}
func (b *Bridge) HasSession(chat string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions[chat] != nil
}

func (b *Bridge) SetHandler(chat string, handler BridgeHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.sessions[chat]; s != nil {
		s.handler = handler
	}
}
func (b *Bridge) Connect(chat, id string, handler BridgeHandler) bool {
	b.mu.Lock()
	if b.ctx.Err() != nil || !migrationFromContext(b.ctx).Available() {
		b.mu.Unlock()
		return false
	}
	if old := b.sessions[chat]; old != nil && old.socket != nil && !old.ended {
		old.handler = handler
		b.mu.Unlock()
		return false
	}
	old := b.sessions[chat]
	ctx, cancel := context.WithCancel(b.ctx)
	b.sequence++
	s := &bridgeSession{id: id, cancel: cancel, open: make(chan struct{}), initial: make(chan struct{}), done: make(chan struct{}), handler: handler, lastActivity: b.Now(), pending: map[string]bool{}, ordinal: b.sequence}
	if old != nil {
		s.lastActivity, s.turnActive, s.cliRunning, s.pending, s.ordinal = old.lastActivity, old.turnActive, old.cliRunning, old.pending, old.ordinal
		s.attempts = old.attempts
		old.cancel()
		if old.socket != nil {
			_ = old.socket.Close("session replaced")
		}
	}
	b.sessions[chat] = s
	b.wg.Add(1)
	b.mu.Unlock()
	go func() { defer b.wg.Done(); defer close(s.done); b.run(ctx, chat, s) }()
	return true
}
func (b *Bridge) run(ctx context.Context, chat string, s *bridgeSession) {
	var initial sync.Once
	defer initial.Do(func() { close(s.initial) })
	for {
		address := strings.TrimSuffix(b.config.ServerURL, "/") + "/ws/" + s.id
		u, err := url.Parse(address)
		if err != nil {
			return
		}
		if token := protocol.TrimJavaScriptString(b.config.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"]); token != "" {
			query := u.Query()
			query.Set("token", token)
			u.RawQuery = query.Encode()
		}
		socket, err := DialSocket(ctx, u.String(), b.tls)
		b.mu.Lock()
		current := b.sessions[chat] == s
		if !current || ctx.Err() != nil {
			b.mu.Unlock()
			if socket != nil {
				_ = socket.Close("session reset")
			}
			return
		}
		if err == nil {
			s.socket = socket
			s.attempts = 0
			select {
			case <-s.open:
			default:
				close(s.open)
			}
		}
		b.mu.Unlock()
		initial.Do(func() { close(s.initial) })
		if err == nil {
			for {
				var bytes []byte
				bytes, err = socket.Read()
				if err != nil {
					break
				}
				var message protocol.JSONObject
				if message.UnmarshalJSON(protocol.DecodeJavaScriptUTF8(bytes)) != nil || message.String("type") == "pong" {
					continue
				}
				b.mu.Lock()
				handler := s.handler
				active := b.sessions[chat] == s
				if active {
					b.trackInbound(s, message)
				}
				b.mu.Unlock()
				if active && handler != nil {
					if failure := handler(ctx, message); failure != nil && b.Log != nil {
						b.Log("[WsBridge] Handler error on " + chat + ": " + failure.Error())
					}
				}
			}
			_ = socket.conn.Close()
		}
		b.mu.Lock()
		if b.sessions[chat] != s || ctx.Err() != nil {
			b.mu.Unlock()
			return
		}
		s.socket = nil
		var closed *SocketClose
		if errors.As(err, &closed) && closed.Code == 1000 {
			delete(b.sessions, chat)
			s.ended = true
			b.mu.Unlock()
			return
		}
		if s.attempts >= 10 {
			delete(b.sessions, chat)
			s.ended = true
			b.mu.Unlock()
			return
		}
		s.attempts++
		delay := time.Second * time.Duration(1<<(s.attempts-1))
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		b.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (b *Bridge) WaitOpen(ctx context.Context, chat string) bool {
	b.mu.Lock()
	s := b.sessions[chat]
	if s == nil {
		b.mu.Unlock()
		return false
	}
	if s.socket != nil && !s.ended {
		b.mu.Unlock()
		return true
	}
	open, initial := s.open, s.initial
	b.mu.Unlock()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-open:
		return b.IsOpen(chat, s.id)
	case <-initial:
		return b.IsOpen(chat, s.id)
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}
func (b *Bridge) Send(chat string, message protocol.JSONObject) bool {
	b.mu.Lock()
	s := b.sessions[chat]
	var socket *Socket
	if s != nil && !s.ended {
		socket = s.socket
		if socket != nil && message.String("type") != "ping" {
			s.lastActivity = b.Now()
			if message.String("type") == "user_message" {
				s.turnActive = true
			}
			if message.String("type") == "permission_response" {
				delete(s.pending, message.String("requestId"))
			}
		}
	}
	b.mu.Unlock()
	if socket == nil {
		return false
	}
	data, err := message.MarshalJSON()
	return err == nil && socket.Send(data) == nil
}
func (b *Bridge) SendUser(chat, text string, attachments json.RawMessage) bool {
	return b.SendUserText(chat, NewText(text), attachments)
}
func (b *Bridge) SendUserText(chat string, text Text, attachments json.RawMessage) bool {
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("user_message")}, protocol.JSONField{Name: "content", Value: raw(text)})
	if len(attachments) > 0 && string(attachments) != "[]" {
		message.Set("attachments", attachments)
	}
	return b.Send(chat, message)
}
func (b *Bridge) Reset(chat string) {
	b.mu.Lock()
	s := b.sessions[chat]
	var socket *Socket
	if s != nil {
		socket = s.socket
	}
	delete(b.sessions, chat)
	b.mu.Unlock()
	if s != nil {
		s.cancel()
		if socket != nil {
			_ = socket.Close("session reset")
		}
	}
}
func (b *Bridge) Close() {
	b.cancel()
	b.mu.Lock()
	sockets := []*Socket{}
	sessions := []*bridgeSession{}
	for _, s := range b.sessions {
		sessions = append(sessions, s)
		if s.socket != nil {
			sockets = append(sockets, s.socket)
		}
	}
	b.sessions = map[string]*bridgeSession{}
	b.mu.Unlock()
	for _, s := range sessions {
		s.cancel()

	}
	for _, socket := range sockets {
		_ = socket.Close("adapter shutdown")
	}
	b.wg.Wait()
}

func (b *Bridge) trackInbound(s *bridgeSession, m protocol.JSONObject) {
	s.lastActivity = b.Now()
	switch m.String("type") {
	case "status":
		s.turnActive = m.String("state") != "idle"
	case "message_complete":
		s.turnActive = false
		clear(s.pending)
	case "error":
		s.turnActive = false
	case "permission_request", "computer_use_permission_request":
		if isString(m.Get("requestId")) {
			s.pending[m.String("requestId")] = true
		}
	case "permission_resolved":
		delete(s.pending, m.String("requestId"))
	case "permission_requests_snapshot":
		s.turnActive = string(m.Get("turnActive")) == "true"
		s.pending = map[string]bool{}
		for _, field := range []string{"toolRequestIds", "computerUseRequestIds"} {
			var ids []json.RawMessage
			_ = json.Unmarshal(m.Get(field), &ids)
			for _, id := range ids {
				if isString(id) {
					var value string
					_ = json.Unmarshal(id, &value)
					s.pending[value] = true
				}
			}
		}
	case "system_notification":
		if m.String("subtype") == "session_state_changed" {
			s.cliRunning = object(m.Get("data")).String("state") == "running"
		}
	case "session_state":
		// The shared Go host also emits its native snapshot after reconnect.
		s.turnActive = m.String("turnState") == "running"
		var background []json.RawMessage
		_ = json.Unmarshal(m.Get("activeBackgroundTaskIds"), &background)
		s.cliRunning = len(background) > 0
	}
}
func (b *Bridge) MarkActive(chat string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.sessions[chat]; s != nil {
		s.lastActivity = b.Now()
	}
}
func (b *Bridge) ReleaseIdleSessions() []string {
	b.mu.Lock()
	now := b.Now()
	type released struct {
		chat    string
		session *bridgeSession
	}
	selected := []released{}
	for chat, s := range b.sessions {
		if s.socket == nil || s.ended || s.turnActive || s.cliRunning || len(s.pending) > 0 || now.Sub(s.lastActivity) < b.idleRelease {
			continue
		}
		selected = append(selected, released{chat, s})
		delete(b.sessions, chat)
	}
	b.mu.Unlock()
	sort.Slice(selected, func(i, j int) bool { return selected[i].session.ordinal < selected[j].session.ordinal })
	chats := []string{}
	for _, entry := range selected {
		entry.session.cancel()
		_ = entry.session.socket.Close("session reset")
		chats = append(chats, entry.chat)
		if b.Log != nil {
			b.Log("[WsBridge] Releasing idle session " + entry.session.id + " (chat " + entry.chat + ")")
		}
	}
	return chats
}

const SessionReconnectNotice = "暂时无法连接原会话，已保留会话和工作目录，请稍后重试。"

type SessionRestore struct {
	Status  string
	Session *SessionEntry
}

func RestoreSession(ctx context.Context, chat string, store *SessionStore, http *HTTPClient, bridge *Bridge, handler BridgeHandler, clear func()) (SessionRestore, error) {
	stored := store.Get(chat)
	reset := func() {
		if bridge.HasSession(chat) {
			bridge.Reset(chat)
			clear()
		}
	}
	if stored == nil {
		reset()
		return SessionRestore{Status: "missing"}, nil
	}
	if id := bridge.SessionID(chat); id != "" && id != stored.SessionID {
		reset()
	}
	if bridge.IsOpen(chat, stored.SessionID) {
		bridge.MarkActive(chat)
		return SessionRestore{Status: "restored", Session: stored}, nil
	}
	exists, err := http.SessionExists(ctx, stored.SessionID)
	if err != nil {
		exists = true
	}
	if !exists {
		if err = store.Delete(chat); err != nil {
			return SessionRestore{}, err
		}
		had := bridge.HasSession(chat)
		reset()
		if !had {
			clear()
		}
		return SessionRestore{Status: "missing"}, nil
	}
	bridge.Connect(chat, stored.SessionID, handler)
	if bridge.WaitOpen(ctx, chat) {
		return SessionRestore{Status: "restored", Session: stored}, nil
	}
	return SessionRestore{Status: "unavailable", Session: stored}, nil
}
