package imruntime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const QQFullIntents = 1174409219

type QQCloseAction struct {
	Reconnect                         bool
	Delay                             time.Duration
	ClearSession, RefreshToken, Fatal bool
	Reason                            string
}
type QQReconnectState struct {
	Attempts         int
	LastConnected    int64
	QuickDisconnects int
}

func (s *QQReconnectState) Connected(now int64) { s.Attempts = 0; s.LastConnected = now }
func (s *QQReconnectState) NextDelay(custom time.Duration) time.Duration {
	delays := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute}
	d := custom
	if d == 0 {
		d = delays[min(s.Attempts, len(delays)-1)]
	}
	s.Attempts++
	return d
}
func (s *QQReconnectState) HandleClose(code int, aborted bool, now int64) QQCloseAction {
	a := QQCloseAction{Reconnect: !aborted, Reason: fmt.Sprintf("close code %d", code)}
	switch code {
	case 4914, 4915:
		a.Reconnect = false
		a.Fatal = true
		a.Reason = "offline/sandbox-only"
		if code == 4915 {
			a.Reason = "banned"
		}
		return a
	case 4004:
		a.RefreshToken = true
		a.Reason = "invalid token (4004)"
		return a
	case 4008:
		a.Delay = time.Minute
		a.Reason = "rate limited (4008)"
		return a
	case 4006, 4007, 4009:
		a.ClearSession = true
		a.RefreshToken = true
		a.Reason = map[int]string{4006: "session no longer valid", 4007: "invalid seq on resume", 4009: "session timed out"}[code]
		return a
	}
	if code >= 4900 && code <= 4913 {
		a.ClearSession = true
		a.RefreshToken = true
		a.Reason = fmt.Sprintf("internal error (%d)", code)
		return a
	}
	if now-s.LastConnected < 5000 && s.LastConnected > 0 {
		s.QuickDisconnects++
		if s.QuickDisconnects >= 3 {
			s.QuickDisconnects = 0
			a.Delay = time.Minute
			a.Reason = "too many quick disconnects"
		}
	} else {
		s.QuickDisconnects = 0
	}
	a.Reconnect = !aborted && code != 1000
	return a
}

type QQGatewaySocket interface {
	Send([]byte) error
	Read() ([]byte, error)
	Close(string) error
}
type QQGatewayDial func(context.Context, string, http.Header, *tls.Config) (QQGatewaySocket, error)
type QQGatewayOptions struct {
	Client             *QQClient
	Dial               QQGatewayDial
	TLS                *tls.Config
	OnMessage          func(protocol.JSONObject)
	OnReady, OnResumed func(json.RawMessage)
	OnError            func(error)
	OnRawEvent         func(string, json.RawMessage)
}
type QQGateway struct {
	o                       QQGatewayOptions
	mu                      sync.Mutex
	socket                  QQGatewaySocket
	sessionID, lastSequence json.RawMessage
	reconnect               QQReconnectState
	refresh                 bool
}

func NewQQGateway(o QQGatewayOptions) (*QQGateway, error) {
	if o.Client == nil {
		return nil, errors.New("QQ gateway requires actual client")
	}
	if o.Dial == nil {
		o.Dial = DialQQGatewaySocket
	}
	if o.TLS == nil {
		if tr, ok := o.Client.o.HTTP.Transport.(*http.Transport); ok {
			o.TLS = tr.TLSClientConfig
		}
	}
	return &QQGateway{o: o}, nil
}
func (g *QQGateway) Close() {
	g.mu.Lock()
	s := g.socket
	g.socket = nil
	g.mu.Unlock()
	if s != nil {
		_ = s.Close("")
	}
}
func (g *QQGateway) Run(ctx context.Context) error {
	// QQBot's sync token initialization is outside Gateway.connect's retry.
	if _, e := g.o.Client.GetAccessToken(ctx); e != nil {
		return e
	}
	g.o.Client.StartBackgroundRefresh()
	defer g.Close()
	for ctx.Err() == nil && g.o.Client.ctx.Err() == nil {
		if g.refresh {
			g.o.Client.ClearToken()
			g.refresh = false
		}
		token, e := g.o.Client.GetAccessToken(ctx)
		var address json.RawMessage
		if e == nil {
			result, err := g.o.Client.Request(ctx, "GET", "/gateway", nil)
			e = err
			address = object(result).Get("url")
		}
		var action QQCloseAction
		if e != nil {
			g.o.Client.o.Log("[qqbot] Connection failed: " + e.Error())
			action = QQCloseAction{Reconnect: true}
			if strings.Contains(e.Error(), "Too many requests") || strings.Contains(e.Error(), "100001") {
				action.Delay = time.Minute
			}
		} else {
			socket, err := g.o.Dial(ctx, qqString(address).String(), http.Header{"User-Agent": {QQUserAgent}}, g.o.TLS)
			if err != nil {
				if g.o.OnError != nil {
					g.o.OnError(err)
				}
				action = g.reconnect.HandleClose(1006, ctx.Err() != nil, g.o.Client.o.Now().UnixMilli())
			} else {
				g.mu.Lock()
				g.socket = socket
				g.mu.Unlock()
				g.reconnect.Connected(g.o.Client.o.Now().UnixMilli())
				action = g.connection(ctx, socket, token)
				_ = socket.Close("")
				g.mu.Lock()
				if g.socket == socket {
					g.socket = nil
				}
				g.mu.Unlock()
			}
		}
		if action.ClearSession {
			g.sessionID = nil
			g.lastSequence = nil
		}
		if action.RefreshToken {
			g.refresh = true
		}
		if action.Fatal || !action.Reconnect || g.reconnect.Attempts >= 100 {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-g.o.Client.ctx.Done():
				return context.Cause(g.o.Client.ctx)
			}
		}
		if e := g.o.Client.sleep(ctx, g.reconnect.NextDelay(action.Delay)); e != nil {
			return e
		}
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return context.Cause(g.o.Client.ctx)
}

type qqGatewayRead struct {
	data []byte
	err  error
}

func (g *QQGateway) connection(ctx context.Context, s QQGatewaySocket, token json.RawMessage) QQCloseAction {
	reads := make(chan qqGatewayRead, 1)
	stop := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		for {
			data, e := s.Read()
			select {
			case reads <- qqGatewayRead{data, e}:
			case <-stop:
				return
			}
			if e != nil {
				return
			}
		}
	}()
	defer func() { close(stop); _ = s.Close(""); <-joined }()
	heartbeat := make(chan struct{}, 1)
	var timerStop func()
	var interval time.Duration
	var arm func()
	arm = func() {
		timerStop = g.o.Client.o.Schedule(interval, func() {
			select {
			case heartbeat <- struct{}{}:
			default:
			}
		})
	}
	defer func() {
		if timerStop != nil {
			timerStop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return g.reconnect.HandleClose(1000, true, g.o.Client.o.Now().UnixMilli())
		case <-g.o.Client.ctx.Done():
			return g.reconnect.HandleClose(1000, true, g.o.Client.o.Now().UnixMilli())
		case <-heartbeat:
			seq := g.lastSequence
			if len(seq) == 0 {
				seq = raw(nil)
			}
			if e := s.Send(raw(qqObject(qqField("op", 1), protocol.JSONField{Name: "d", Value: seq}))); e != nil && g.o.OnError != nil {
				g.o.OnError(e)
			}
			arm()
		case item := <-reads:
			if item.err != nil {
				code := 1006
				var closeError *SocketClose
				if errors.As(item.err, &closeError) {
					code = closeError.Code
				} else if g.o.OnError != nil {
					g.o.OnError(item.err)
				}
				return g.reconnect.HandleClose(code, false, g.o.Client.o.Now().UnixMilli())
			}
			value, e := qqParse(item.data)
			if e != nil {
				g.o.Client.o.Log("[qqbot] Message parse error: " + e.Error())
				continue
			}
			payload := object(value)
			if truthy(payload.Get("s")) {
				g.lastSequence = append(json.RawMessage(nil), payload.Get("s")...)
			}
			data := payload.Get("d")
			switch qqInt(payload.Get("op"), -1) {
			case 10:
				body := qqObject(qqField("token", "QQBot "+qqString(token).String()))
				op := 2
				if truthy(g.sessionID) && len(g.lastSequence) > 0 && string(g.lastSequence) != "null" {
					op = 6
					body.Set("session_id", g.sessionID)
					body.Set("seq", g.lastSequence)
				} else {
					body.Set("intents", raw(QQFullIntents))
					body.Set("shard", raw([]int{0, 1}))
				}
				if e = s.Send(raw(qqObject(qqField("op", op), qqField("d", body)))); e != nil && g.o.OnError != nil {
					g.o.OnError(e)
				}
				if timerStop != nil {
					timerStop()
				}
				interval = time.Duration(max(qqInt(object(data).Get("heartbeat_interval"), 1), 1)) * time.Millisecond
				arm()
			case 0:
				event := payload.String("t")
				switch event {
				case "READY":
					g.sessionID = append(json.RawMessage(nil), object(data).Get("session_id")...)
					if g.o.OnReady != nil {
						g.o.OnReady(append(json.RawMessage(nil), data...))
					}
				case "RESUMED":
					if g.o.OnResumed != nil {
						g.o.OnResumed(append(json.RawMessage(nil), data...))
					} else if g.o.OnReady != nil {
						g.o.OnReady(append(json.RawMessage(nil), data...))
					}
				default:
					if message, ok := QQDispatchC2C(event, object(data)); ok {
						if g.o.OnMessage != nil {
							g.o.OnMessage(message)
						}
					} else if g.o.OnRawEvent != nil {
						g.o.OnRawEvent(event, append(json.RawMessage(nil), data...))
					}
				}
			case 7:
				g.reconnect.HandleClose(1000, false, g.o.Client.o.Now().UnixMilli())
				return QQCloseAction{Reconnect: true}
			case 9:
				canResume := truthy(data)
				g.reconnect.HandleClose(1000, false, g.o.Client.o.Now().UnixMilli())
				return QQCloseAction{Reconnect: true, Delay: 3 * time.Second, ClearSession: !canResume, RefreshToken: !canResume}
			}
		}
	}
}
