package ccrsessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

// Manager is the client subscription used by RemoteSessionManager. It is
// separate from RemoteIO, whose worker protocol uses a different endpoint,
// reconnect policy, and persistence sink. Callbacks transfer detached frames
// to the existing application Owner; no Agent execution occurs on this worker.
type ManagerCallbacks struct {
	Message func(protocol.JSONObject)
	State   func(string)
	Error   func(error)
}

type managerDialAttempt struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type Manager struct {
	service   *Service
	sid       string
	callbacks ManagerCallbacks
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	start     chan struct{}
	startOnce sync.Once
	mu        sync.Mutex
	writeMu   sync.Mutex
	workers   sync.WaitGroup
	conn      *websocket.Conn
	dial      *managerDialAttempt
	closed    bool
	forced    bool
}

func (m *Manager) beginDial() (*managerDialAttempt, error) {
	ctx, cancel := context.WithCancel(m.ctx)
	attempt := &managerDialAttempt{ctx: ctx, cancel: cancel}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return nil, context.Canceled
	}
	m.dial = attempt
	m.mu.Unlock()
	return attempt, nil
}

// A force reconnect retires the exact pending native attempt before cancelling
// it. A handshake that completed concurrently cannot publish its old socket.
func (m *Manager) finishDial(attempt *managerDialAttempt, conn *websocket.Conn, err error) error {
	m.mu.Lock()
	current := m.dial == attempt
	if current {
		m.dial = nil
	}
	retired := !current || m.closed || attempt.ctx.Err() != nil
	if err == nil && !retired {
		m.conn = conn
	}
	m.mu.Unlock()
	if err == nil && retired {
		_ = conn.CloseNow()
		return context.Canceled
	}
	return err
}

func (s *Service) NewRemoteManager(ctx context.Context, sid string, callbacks ManagerCallbacks) (*Manager, error) {
	if sid == "" || strings.ContainsAny(sid, "/?#") {
		return nil, errors.New("CCR subscription requires a session identity")
	}
	if _, err := s.credentials(ctx, true); err != nil {
		return nil, err
	}
	life, done, err := s.operation(ctx)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(life)
	m := &Manager{service: s, sid: sid, callbacks: callbacks, ctx: life, cancel: cancel, done: make(chan struct{}), start: make(chan struct{})}
	go func() {
		defer done()
		defer close(m.done)
		select {
		case <-m.ctx.Done():
			return
		case <-m.start:
			m.run()
		}
	}()
	return m, nil
}
func (m *Manager) Start() { m.startOnce.Do(func() { close(m.start) }) }
func (m *Manager) state(value string) {
	if m.callbacks.State != nil {
		m.callbacks.State(value)
	}
}
func (m *Manager) failure(err error) {
	if err != nil && m.callbacks.Error != nil {
		m.callbacks.Error(err)
	}
	m.service.report(err)
}
func (m *Manager) operation(ctx context.Context) (context.Context, func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, context.Canceled
	}
	m.workers.Add(1)
	m.mu.Unlock()
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	return child, func() { stop(); cancel(); m.workers.Done() }, nil
}
func (m *Manager) run() {
	reconnectAttempts, notFoundRetries := 0, 0
	for m.ctx.Err() == nil {
		m.state("connecting")
		credentials, err := m.service.credentials(m.ctx, false)
		var conn *websocket.Conn
		stopDial := func() {}
		if err == nil {
			u, _ := url.Parse(m.service.options.BaseURL)
			if u.Scheme == "https" {
				u.Scheme = "wss"
			} else {
				u.Scheme = "ws"
			}
			u.Path += "/v1/sessions/ws/" + url.PathEscape(m.sid) + "/subscribe"
			u.RawQuery = "organization_uuid=" + url.QueryEscape(credentials.Organization)
			var attempt *managerDialAttempt
			attempt, err = m.beginDial()
			if err == nil {
				stopDial = attempt.cancel
				conn, _, err = websocket.Dial(attempt.ctx, u.String(), &websocket.DialOptions{HTTPClient: m.service.options.HTTP, HTTPHeader: http.Header{"Authorization": {"Bearer " + credentials.Token}, "anthropic-version": {"2023-06-01"}}})
				err = m.finishDial(attempt, conn, err)
			}
		}
		connected := err == nil
		if connected {
			conn.SetReadLimit(-1)
			m.mu.Lock()
			if m.closed {
				m.mu.Unlock()
				_ = conn.CloseNow()
				stopDial()
				return
			}
			m.mu.Unlock()
			reconnectAttempts, notFoundRetries = 0, 0
			m.state("connected")
			connection, stop := context.WithCancel(m.ctx)
			pingDone := make(chan struct{})
			go func() {
				defer close(pingDone)
				timer := time.NewTicker(30 * time.Second)
				defer timer.Stop()
				for {
					select {
					case <-connection.Done():
						return
					case <-timer.C:
						_ = conn.Ping(connection)
					}
				}
			}()
			for {
				_, data, readErr := conn.Read(connection)
				if readErr != nil {
					err = readErr
					break
				}
				var frame protocol.JSONObject
				if frame.UnmarshalJSON(data) != nil || !frame.IsObject() || frame.Get("type") == nil || frame.Get("type")[0] != '"' {
					continue
				}
				if m.callbacks.Message != nil {
					m.callbacks.Message(frame)
				}
			}
			stop()
			_ = conn.CloseNow()
			<-pingDone
			m.mu.Lock()
			if m.conn == conn {
				m.conn = nil
			}
			m.mu.Unlock()
		}
		stopDial()
		if m.ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		forced := m.forced
		m.forced = false
		m.mu.Unlock()
		code := int(websocket.CloseStatus(err))
		delay := 2 * time.Second
		if forced {
			reconnectAttempts, notFoundRetries = 0, 0
			delay = 500 * time.Millisecond
		} else if code == 4003 {
			m.state("disconnected")
			return
		} else if code == 4001 {
			notFoundRetries++
			if notFoundRetries > 3 {
				m.state("disconnected")
				return
			}
			delay *= time.Duration(notFoundRetries)
		} else if connected && reconnectAttempts < 5 {
			reconnectAttempts++
		} else {
			m.failure(err)
			m.state("disconnected")
			return
		}
		m.state("reconnecting")
		if m.service.options.Wait(m.ctx, delay) != nil {
			return
		}
	}
}
func (m *Manager) SendControl(ctx context.Context, frame protocol.JSONObject) error {
	ctx, done, err := m.operation(ctx)
	if err != nil {
		return err
	}
	defer done()
	data, err := frame.MarshalJSON()
	if err != nil {
		return err
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	m.mu.Lock()
	conn, closed := m.conn, m.closed
	m.mu.Unlock()
	if closed {
		return context.Canceled
	}
	if conn == nil {
		return errors.New("[SessionsWebSocket] Cannot send: not connected")
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
func (m *Manager) QueueControl(frame protocol.JSONObject) error {
	ctx, done, err := m.operation(context.Background())
	if err != nil {
		return err
	}
	go func() { defer done(); m.failure(m.SendControl(ctx, frame)) }()
	return nil
}
func (m *Manager) SendMessage(ctx context.Context, content json.RawMessage, messageID string) (bool, error) {
	ctx, done, err := m.operation(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	credentials, err := m.service.credentials(ctx, true)
	if err != nil {
		return false, err
	}
	if messageID == "" {
		messageID = uuid()
	}
	event := map[string]any{"uuid": messageID, "session_id": m.sid, "type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": content}}
	body, err := json.Marshal(map[string]any{"events": []any{event}})
	if err != nil {
		return false, err
	}
	_, status, err := m.service.request(ctx, "POST", "/v1/sessions/"+m.sid+"/events", body, credentials, true, 30*time.Second, nil)
	if err != nil {
		return false, err
	}
	if status != 200 && status != 201 {
		return false, fmt.Errorf("[RemoteSessionManager] Failed to send message: %d", status)
	}
	return true, nil
}
func (m *Manager) Reconnect() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.forced = true
	conn := m.conn
	attempt := m.dial
	if attempt != nil {
		m.dial = nil
	}
	m.mu.Unlock()
	if attempt != nil {
		attempt.cancel()
	}
	if conn != nil {
		_ = conn.CloseNow()
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	conn := m.conn
	m.mu.Unlock()
	m.cancel()
	if conn != nil {
		_ = conn.CloseNow()
	}
	<-m.done
	m.workers.Wait()
}
