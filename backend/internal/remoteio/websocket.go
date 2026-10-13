package remoteio

import (
	"context"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

func (s *Service) webSocketLoop() {
	var reconnectStart, lastAttempt time.Time
	attempt := 0
	headers := s.headers(false)
	for s.ctx.Err() == nil {
		s.mu.Lock()
		last := s.lastSentID
		s.mu.Unlock()
		if last != "" {
			headers.Set("X-Last-Request-Id", last)
		}
		client := s.options.HTTP
		var err error
		if s.options.WebSocketHTTP != nil {
			client, err = s.options.WebSocketHTTP(s.environmentSnapshot())
		}
		var conn *websocket.Conn
		if err == nil {
			conn, _, err = websocket.Dial(s.ctx, s.endpoints.Stream, &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers})
		}
		code := 0
		if err == nil {
			conn.SetReadLimit(-1)
			s.wsWriteMu.Lock()
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				s.wsWriteMu.Unlock()
				_ = conn.CloseNow()
				return
			}
			s.conn = conn
			buffer := append([]protocol.JSONObject(nil), s.buffer...)
			replayLast := s.lastSentID
			s.mu.Unlock()
			reconnectStart, lastAttempt = time.Time{}, time.Time{}
			attempt = 0
			// Bun's transport replays all buffered UUID-bearing frames on open;
			// server-side UUID admission owns deduplication.
			if replayLast != "" && s.endpoints.Kind == WebSocket {
				for _, message := range buffer {
					data, _ := protocol.EncodeNDJSON(message)
					if conn.Write(s.ctx, websocket.MessageText, data) != nil {
						break
					}
				}
			}
			s.wsWriteMu.Unlock()
			if s.options.OnConnect != nil {
				s.options.OnConnect()
			}
			connectionContext, cancel := context.WithCancel(s.ctx)
			var timers sync.WaitGroup
			timers.Add(1)
			go func() { defer timers.Done(); s.wsTimers(connectionContext, conn) }()
			for {
				_, data, failure := conn.Read(connectionContext)
				if failure != nil {
					err = failure
					code = int(websocket.CloseStatus(failure))
					break
				}
				s.receive(data)
			}
			cancel()
			_ = conn.CloseNow()
			timers.Wait()
			s.mu.Lock()
			if s.conn == conn {
				s.conn = nil
			}
			s.mu.Unlock()
		}
		if s.options.WebSocketHTTP != nil && client != nil {
			client.CloseIdleConnections()
		}
		if s.ctx.Err() != nil {
			return
		}
		refreshed := false
		if code == 4003 {
			next := s.headers(false)
			if next.Get("Authorization") != headers.Get("Authorization") {
				for key, value := range next {
					headers[key] = value
				}
				refreshed = true
			}
		}
		if PermanentClose(code) && !refreshed {
			s.finish(code, nil)
			return
		}
		now := s.options.Now()
		if reconnectStart.IsZero() {
			reconnectStart = now
		}
		if !lastAttempt.IsZero() && now.Sub(lastAttempt) > 60*time.Second {
			reconnectStart = now
			attempt = 0
		}
		lastAttempt = now
		if now.Sub(reconnectStart) >= 10*time.Minute {
			s.finish(code, nil)
			return
		}
		if !refreshed {
			for key, value := range s.headers(false) {
				headers[key] = value
			}
		}
		attempt++
		s.diagnostic("cli_websocket_reconnect_attempt")
		if s.options.Wait(s.ctx, s.retryDelay(attempt, 30*time.Second, 0.25)) != nil {
			return
		}
	}
}

func (s *Service) wsTimers(ctx context.Context, conn *websocket.Conn) {
	ping := time.NewTicker(10 * time.Second)
	defer ping.Stop()
	alive := time.NewTicker(5 * time.Minute)
	defer alive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			pingContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingContext)
			cancel()
			if err != nil {
				_ = conn.CloseNow()
				return
			}
		case <-alive.C:
			if conn.Write(ctx, websocket.MessageText, []byte("{\"type\":\"keep_alive\"}\n")) != nil {
				_ = conn.CloseNow()
				return
			}
		}
	}
}

func (s *Service) writeWebSocket(message protocol.JSONObject) error {
	s.wsWriteMu.Lock()
	defer s.wsWriteMu.Unlock()
	data, err := protocol.EncodeNDJSON(message)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if value := message.Get("uuid"); len(value) > 0 && value[0] == '"' {
		messageID := message.String("uuid")
		// The original buffer owns the emitted message, not prepared output.
		var owned protocol.JSONObject
		_ = owned.UnmarshalJSON(raw(message))
		s.buffer = append(s.buffer, owned)
		if len(s.buffer) > 1000 {
			s.buffer = s.buffer[len(s.buffer)-1000:]
		}
		s.lastSentID = messageID
	}
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		if err := conn.Write(s.ctx, websocket.MessageText, data); err != nil {
			_ = conn.CloseNow()
		}
	}
	return nil
}
