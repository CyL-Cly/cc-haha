package imruntime

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sync"
	"time"
)

type SlackSocketOptions struct {
	OpenConnection func(context.Context) (string, error)
	OnEnvelope     func(protocol.JSONObject)
	TLS            *tls.Config
	Log            func(string)
}
type SlackSocketMode struct {
	o            SlackSocketOptions
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	socket       *Socket
	wg           sync.WaitGroup
	start, close sync.Once
}

func NewSlackSocketMode(ctx context.Context, o SlackSocketOptions) *SlackSocketMode {
	if o.Log == nil {
		o.Log = func(string) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &SlackSocketMode{o: o, ctx: ctx, cancel: cancel}
}
func (s *SlackSocketMode) Start() {
	s.start.Do(func() { s.wg.Add(1); go func() { defer s.wg.Done(); s.run() }() })
}
func SlackReconnectDelay(attempt int) time.Duration {
	if attempt >= 6 {
		return 30 * time.Second
	}
	return time.Second * time.Duration(1<<max(0, attempt-1))
}
func (s *SlackSocketMode) run() {
	attempts := 0
	for s.ctx.Err() == nil {
		address, err := s.o.OpenConnection(s.ctx)
		var socket *Socket
		if err != nil {
			s.o.Log("[Slack] Failed to open Socket Mode connection: " + err.Error())
		} else if s.ctx.Err() == nil {
			socket, err = DialSocket(s.ctx, address, s.o.TLS)
			if err != nil {
				s.o.Log("[Slack] Socket error: " + err.Error())
			}
		}
		if socket != nil {
			s.mu.Lock()
			if s.ctx.Err() != nil {
				s.mu.Unlock()
				_ = socket.conn.Close()
				return
			}
			s.socket = socket
			s.mu.Unlock()
			attempts = 0
			s.o.Log("[Slack] Socket Mode connected")
			for s.ctx.Err() == nil {
				data, err := socket.Read()
				if err != nil {
					break
				}
				var e protocol.JSONObject
				if e.UnmarshalJSON(protocol.DecodeJavaScriptUTF8(data)) != nil {
					s.o.Log("[Slack] Socket parse error")
					continue
				}
				if truthy(e.Get("envelope_id")) {
					_ = socket.Send(raw(protocol.NewJSONObject(protocol.JSONField{Name: "envelope_id", Value: e.Get("envelope_id")})))
				}
				if e.String("type") == "disconnect" {
					reason := e.String("reason")
					if len(e.Get("reason")) == 0 || string(e.Get("reason")) == "null" {
						reason = "unknown"
					}
					s.o.Log("[Slack] Slack asked to reconnect (" + reason + ")")
					_ = socket.Close("slack requested reconnect")
					break
				}
				if e.String("type") == "hello" {
					continue
				}
				if s.o.OnEnvelope != nil {
					s.o.OnEnvelope(e)
				}
			}
			_ = socket.conn.Close()
			s.mu.Lock()
			if s.socket == socket {
				s.socket = nil
			}
			s.mu.Unlock()
		}
		if s.ctx.Err() != nil {
			return
		}
		attempts++
		delay := SlackReconnectDelay(attempts)
		s.o.Log(fmt.Sprintf("[Slack] Reconnecting Socket Mode in %dms (attempt %d)", delay.Milliseconds(), attempts))
		timer := time.NewTimer(delay)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (s *SlackSocketMode) Close() {
	s.close.Do(func() {
		s.cancel()
		s.mu.Lock()
		socket := s.socket
		s.socket = nil
		s.mu.Unlock()
		if socket != nil {
			_ = socket.Close("adapter stopped")
		}
		s.wg.Wait()
	})
}
