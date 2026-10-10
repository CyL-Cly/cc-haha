package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type BridgeHandler func(context.Context, protocol.JSONObject) error
type bridgeSession struct {
	id       string
	socket   *Socket
	cancel   context.CancelFunc
	open     chan struct{}
	initial  chan struct{}
	done     chan struct{}
	handler  BridgeHandler
	attempts int
	ended    bool
}
type Bridge struct {
	config   Config
	tls      *tls.Config
	mu       sync.Mutex
	sessions map[string]*bridgeSession
	closed   bool
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	Log      func(string)
}

func NewBridge(ctx context.Context, config Config, tlsConfig *tls.Config, log func(string)) *Bridge {
	ctx, cancel := context.WithCancel(ctx)
	b := &Bridge{config: config, tls: tlsConfig, sessions: map[string]*bridgeSession{}, ctx: ctx, cancel: cancel, Log: log}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		timer := time.NewTicker(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
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
	if b.ctx.Err() != nil {
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
	s := &bridgeSession{id: id, cancel: cancel, open: make(chan struct{}), initial: make(chan struct{}), done: make(chan struct{}), handler: handler}
	if old != nil {
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
