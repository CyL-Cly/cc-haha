package imruntime

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const WecomWebSocketURL = "wss://openws.work.weixin.qq.com"

type WecomClientOptions struct {
	BotID, Secret Text
	// Source env || adapters.json values enter subscribe unchanged, including
	// non-string JSON values. When present, these two raw fields win over Text.
	Credentials protocol.JSONObject
	HTTP        *http.Client
	Environment map[string]string
	TLS         *tls.Config
	// Dial is a physical transport seam. Its address is always the official URL;
	// tests may route that connection to a private TLS peer without a product URL override.
	Dial                                                            func(context.Context, string, *tls.Config) (*Socket, error)
	RequestID                                                       func(string) Text
	Now                                                             func() time.Time
	HeartbeatInterval, ReconnectBaseDelay, ReplyAckTimeout          time.Duration
	RequestTimeout                                                  *time.Duration
	MaxReconnectAttempts, MaxAuthFailureAttempts, MaxReplyQueueSize *int
	OnMessage, OnEvent                                              func(protocol.JSONObject)
	OnAuthenticated                                                 func()
	OnConnected                                                     func()
	OnServerDisconnect                                              func(string)
	OnDisconnected                                                  func(string)
	OnReconnecting                                                  func(int)
	OnError                                                         func(error)
}
type WecomLimitError struct {
	Code     string
	Attempts int
}

func (e *WecomLimitError) Error() string {
	if e.Code == "WS_AUTH_FAILURE_EXHAUSTED" {
		return fmt.Sprintf("Max auth failure attempts exceeded (%d)", e.Attempts)
	}
	return fmt.Sprintf("Max reconnect attempts exceeded (%d)", e.Attempts)
}

type WecomAckError struct{ Frame protocol.JSONObject }

func (e *WecomAckError) Error() string { return string(raw(e.Frame)) }

type wecomReplyResult struct {
	frame protocol.JSONObject
	err   error
}
type wecomReply struct {
	frame   protocol.JSONObject
	ack     chan wecomReplyResult
	done    chan wecomReplyResult
	pending bool
}
type WecomClient struct {
	o                                      WecomClientOptions
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	mu                                     sync.Mutex
	socket                                 *Socket
	queues                                 map[string][]*wecomReply
	closed                                 bool
	wg                                     sync.WaitGroup
	done                                   chan struct{}
	start, close                           sync.Once
	reconnectAttempts, authFailureAttempts int
	HTTP                                   *http.Client
}

func NewWecomClient(ctx context.Context, o WecomClientOptions) (*WecomClient, error) {
	if o.Dial == nil {
		o.Dial = DialSocket
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RequestID == nil {
		o.RequestID = func(prefix string) Text {
			var b [4]byte
			_, _ = rand.Read(b[:])
			return NewText(fmt.Sprintf("%s_%d_%s", prefix, o.Now().UnixMilli(), hex.EncodeToString(b[:])))
		}
	}
	if o.HeartbeatInterval == 0 {
		o.HeartbeatInterval = 30 * time.Second
	}
	if o.ReconnectBaseDelay == 0 {
		o.ReconnectBaseDelay = time.Second
	}
	if o.ReplyAckTimeout == 0 {
		o.ReplyAckTimeout = 5 * time.Second
	}
	if o.RequestTimeout == nil {
		timeout := 10 * time.Second
		o.RequestTimeout = &timeout
	}
	if o.MaxReconnectAttempts == nil {
		n := 10
		o.MaxReconnectAttempts = &n
	}
	if o.MaxAuthFailureAttempts == nil {
		n := 5
		o.MaxAuthFailureAttempts = &n
	}
	if o.MaxReplyQueueSize == nil {
		n := 500
		o.MaxReplyQueueSize = &n
	}
	client := o.HTTP
	if client == nil {
		var err error
		client, err = AdapterNetworkHTTP(o.Environment, true)
		if err != nil {
			return nil, err
		}
	}
	if o.TLS == nil {
		if tr, ok := client.Transport.(*http.Transport); ok {
			o.TLS = tr.TLSClientConfig
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &WecomClient{o: o, ctx: ctx, cancel: cancel, queues: map[string][]*wecomReply{}, done: make(chan struct{}), HTTP: client}, nil
}
func (c *WecomClient) emitError(err error) {
	if c.o.OnError != nil {
		c.o.OnError(err)
	}
}
func (c *WecomClient) Start() {
	c.start.Do(func() {
		c.mu.Lock()
		if c.closed {
			close(c.done)
			c.mu.Unlock()
			return
		}
		c.wg.Add(1)
		c.mu.Unlock()
		go func() { defer c.wg.Done(); defer close(c.done); c.run() }()
	})
}
func (c *WecomClient) Wait() error { <-c.done; return nil }
func (c *WecomClient) Close() {
	c.close.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		socket := c.socket
		c.socket = nil
		c.clearRepliesLocked("Connection manually closed")
		c.mu.Unlock()
		if socket != nil {
			_ = socket.conn.Close()
		}
		c.start.Do(func() { close(c.done) })
		c.wg.Wait()
		c.HTTP.CloseIdleConnections()
	})
}
func (c *WecomClient) begin(ctx context.Context) (context.Context, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, context.Canceled
	}
	c.wg.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	return ctx, func() { stop(); cancel(); c.wg.Done() }, nil
}
func wecomFrame(cmd string, reqID Text, body *protocol.JSONObject) protocol.JSONObject {
	f := wecomJSONObject(wecomField("cmd", cmd), wecomField("headers", wecomJSONObject(wecomField("req_id", reqID))))
	if body != nil {
		f.Set("body", raw(*body))
	}
	return f
}
func (c *WecomClient) run() {
	for c.ctx.Err() == nil {
		socket, err := c.o.Dial(c.ctx, WecomWebSocketURL, c.o.TLS)
		authFailure, manual := false, false
		reason := "code: 1006"
		if err != nil {
			if c.ctx.Err() == nil {
				c.emitError(err)
			}
		} else {
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				_ = socket.conn.Close()
				return
			}
			c.socket = socket
			c.mu.Unlock()
			authFailure, manual, reason = c.connection(socket)
			_ = socket.conn.Close()
			c.mu.Lock()
			if c.socket == socket {
				c.socket = nil
			}
			c.clearRepliesLocked("WebSocket connection closed (" + reason + ")")
			c.mu.Unlock()
			if c.o.OnDisconnected != nil && !manual && c.ctx.Err() == nil {
				c.o.OnDisconnected(reason)
			}
		}
		if manual || c.ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		var attempts, maxAttempts int
		var code string
		if authFailure {
			attempts = c.authFailureAttempts
			maxAttempts = *c.o.MaxAuthFailureAttempts
			code = "WS_AUTH_FAILURE_EXHAUSTED"
		} else {
			attempts = c.reconnectAttempts
			maxAttempts = *c.o.MaxReconnectAttempts
			code = "WS_RECONNECT_EXHAUSTED"
		}
		if maxAttempts != -1 && attempts >= maxAttempts {
			c.mu.Unlock()
			c.emitError(&WecomLimitError{code, maxAttempts})
			<-c.ctx.Done()
			return
		}
		attempts++
		if authFailure {
			c.authFailureAttempts = attempts
		} else {
			c.reconnectAttempts = attempts
		}
		c.mu.Unlock()
		if c.o.OnReconnecting != nil {
			c.o.OnReconnecting(attempts)
		}
		delay := c.o.ReconnectBaseDelay
		for i := 1; i < attempts && delay < 30*time.Second; i++ {
			delay *= 2
		}
		delay = min(delay, 30*time.Second)
		timer := time.NewTimer(delay)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (c *WecomClient) closeReason(err error) string {
	if err == nil {
		return "connection closed"
	}
	var closeErr *SocketClose
	if errors.As(err, &closeErr) {
		if closeErr.Reason != "" {
			return closeErr.Reason
		}
		return fmt.Sprintf("code: %d", closeErr.Code)
	}
	return err.Error()
}

type wecomRead struct {
	data []byte
	err  error
}

func (c *WecomClient) connection(socket *Socket) (authFailure, manual bool, reason string) {
	reads := make(chan wecomRead, 1)
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			data, err := socket.Read()
			select {
			case reads <- wecomRead{data, err}:
			case <-stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { close(stop); _ = socket.conn.Close(); <-readerDone }()
	body := wecomJSONObject(wecomField("bot_id", c.o.BotID), wecomField("secret", c.o.Secret))
	for _, name := range []string{"bot_id", "secret"} {
		if v := c.o.Credentials.Get(name); len(v) > 0 {
			body.Set(name, v)
		}
	}
	if err := socket.Send(wecomWire(wecomFrame("aibot_subscribe", c.o.RequestID("aibot_subscribe"), &body))); err != nil {
		c.emitError(err)
	}
	if c.o.OnConnected != nil {
		c.o.OnConnected()
	}
	var ticker *time.Ticker
	var heartbeat <-chan time.Time
	missed := 0
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	for {
		select {
		case <-c.ctx.Done():
			return false, true, ""
		case <-heartbeat:
			if missed >= 2 {
				return false, false, "Connection ended"
			}
			missed++
			if err := socket.Send(wecomWire(wecomFrame("ping", c.o.RequestID("ping"), nil))); err != nil {
				c.emitError(err)
			}
		case item := <-reads:
			if item.err != nil {
				reason := "code: 1006"
				var closed *SocketClose
				if errors.As(item.err, &closed) {
					reason = c.closeReason(item.err)
				}
				return false, false, reason
			}
			var f protocol.JSONObject
			if f.UnmarshalJSON(wecomStripControl(item.data)) != nil {
				continue
			}
			cmd := f.String("cmd")
			id := wecomReqID(f)
			if cmd == "aibot_msg_callback" || cmd == "aibot_event_callback" {
				body := object(f.Get("body"))
				if truthy(body.Get("msgtype")) {
					if cmd == "aibot_msg_callback" && c.o.OnMessage != nil {
						c.o.OnMessage(object(raw(f)))
					}
					if cmd == "aibot_event_callback" && c.o.OnEvent != nil {
						c.o.OnEvent(object(raw(f)))
					}
				}
				if cmd == "aibot_event_callback" && object(body.Get("event")).String("eventtype") == "disconnected_event" {
					c.mu.Lock()
					c.clearRepliesLocked("Server disconnected due to new connection")
					c.mu.Unlock()
					if c.o.OnServerDisconnect != nil {
						c.o.OnServerDisconnect("New connection established, server disconnected this connection")
					}
					if c.o.OnDisconnected != nil {
						c.o.OnDisconnected("New connection established, server disconnected this connection")
					}
					return false, true, ""
				}
				continue
			}
			if strings.HasPrefix(id.String(), "aibot_subscribe") {
				if !wecomStrictZero(f.Get("errcode")) {
					c.emitError(fmt.Errorf("Authentication failed: %s (code: %s)", wecomJSField(f, "errmsg"), wecomJSField(f, "errcode")))
					return true, false, "Connection ended"
				}
				c.mu.Lock()
				c.authFailureAttempts = 0
				c.reconnectAttempts = 0
				c.mu.Unlock()
				if ticker != nil {
					ticker.Stop()
				}
				ticker = time.NewTicker(c.o.HeartbeatInterval)
				heartbeat = ticker.C
				if c.o.OnAuthenticated != nil {
					c.o.OnAuthenticated()
				}
				continue
			}
			if strings.HasPrefix(id.String(), "ping") {
				if wecomStrictZero(f.Get("errcode")) {
					missed = 0
				}
				continue
			}
			c.mu.Lock()
			queue := c.queues[wecomKey(id)]
			if len(queue) > 0 && queue[0].pending {
				result := wecomReplyResult{frame: f}
				if !wecomStrictZero(f.Get("errcode")) {
					result.err = &WecomAckError{object(raw(f))}
				}
				select {
				case queue[0].ack <- result:
				default:
				}
			}
			c.mu.Unlock()
		}
	}
}
func wecomJSField(f protocol.JSONObject, name string) string {
	v := f.Get(name)
	if len(v) == 0 {
		return "undefined"
	}
	s, err := protocol.JavaScriptStringJSON(v)
	if err != nil {
		return "undefined"
	}
	return TextJSON(s).String()
}
func (c *WecomClient) clearRepliesLocked(reason string) {
	for key, queue := range c.queues {
		var id Text
		if len(queue) > 0 {
			id = wecomReqID(queue[0].frame)
		}
		err := fmt.Errorf("%s, reply for reqId: %s cancelled", reason, id.String())
		for _, item := range queue {
			select {
			case item.ack <- wecomReplyResult{err: err}:
			default:
			}
			select {
			case item.done <- wecomReplyResult{err: err}:
			default:
			}
		}
		delete(c.queues, key)
	}
}
func (c *WecomClient) HasPendingReplyAck(frame protocol.JSONObject) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	q := c.queues[wecomKey(wecomReqID(frame))]
	return len(q) > 0 && q[0].pending
}
func (c *WecomClient) SendReply(ctx context.Context, reqID Text, body protocol.JSONObject, cmd string) (protocol.JSONObject, error) {
	if cmd == "" {
		cmd = "aibot_respond_msg"
	}
	key := wecomKey(reqID)
	item := &wecomReply{frame: wecomFrame(cmd, reqID, &body), ack: make(chan wecomReplyResult, 1), done: make(chan wecomReplyResult, 1)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return protocol.JSONObject{}, context.Canceled
	}
	q := c.queues[key]
	if len(q) >= *c.o.MaxReplyQueueSize {
		c.mu.Unlock()
		return protocol.JSONObject{}, fmt.Errorf("Reply queue for reqId %s exceeds max size (%d)", reqID.String(), *c.o.MaxReplyQueueSize)
	}
	c.queues[key] = append(q, item)
	if len(q) == 0 {
		c.wg.Add(1)
		go c.replyWorker(key)
	}
	c.mu.Unlock()
	select {
	case result := <-item.done:
		return result.frame, result.err
	case <-ctx.Done():
		return protocol.JSONObject{}, ctx.Err()
	case <-c.ctx.Done():
		select {
		case result := <-item.done:
			return result.frame, result.err
		default:
			return protocol.JSONObject{}, c.ctx.Err()
		}
	}
}
func (c *WecomClient) replyWorker(key string) {
	defer c.wg.Done()
	for {
		c.mu.Lock()
		q := c.queues[key]
		if len(q) == 0 {
			c.mu.Unlock()
			return
		}
		item := q[0]
		item.pending = true
		socket := c.socket
		c.mu.Unlock()
		var sendErr error
		if socket == nil {
			sendErr = errors.New("WebSocket not connected, unable to send data")
		} else {
			sendErr = socket.Send(wecomWire(item.frame))
		}
		result := wecomReplyResult{err: sendErr}
		if sendErr == nil {
			timer := time.NewTimer(c.o.ReplyAckTimeout)
			select {
			case result = <-item.ack:
			case <-timer.C:
				result.err = fmt.Errorf("Reply ack timeout (%dms) for reqId: %s", c.o.ReplyAckTimeout.Milliseconds(), wecomReqID(item.frame).String())
			case <-c.ctx.Done():
				result.err = c.ctx.Err()
			}
			timer.Stop()
		}
		c.mu.Lock()
		q = c.queues[key]
		own := len(q) > 0 && q[0] == item
		more := own && len(q) > 1
		if own {
			c.queues[key] = q[1:]
			if len(q) == 1 {
				delete(c.queues, key)
			}
		}
		item.pending = false
		select {
		case item.done <- result:
		default:
		}
		c.mu.Unlock()
		if !more {
			return
		}
	}
}
func (c *WecomClient) ReplyStream(ctx context.Context, frame protocol.JSONObject, id, content Text, finish bool) (protocol.JSONObject, error) {
	body := wecomJSONObject(wecomField("msgtype", "stream"), wecomField("stream", wecomJSONObject(wecomField("id", id), wecomField("finish", finish), wecomField("content", content))))
	return c.SendReply(ctx, wecomReqID(frame), body, "")
}
func (c *WecomClient) SendMessage(ctx context.Context, chat Text, body protocol.JSONObject) (protocol.JSONObject, error) {
	full := wecomJSONObject(wecomField("chatid", chat))
	for _, f := range body.OwnProperties() {
		_ = full.SetPropertyJSON(f.NameJSON, f.Value)
	}
	return c.SendReply(ctx, c.o.RequestID("aibot_send_msg"), full, "aibot_send_msg")
}
func (c *WecomClient) SendMediaMessage(ctx context.Context, chat Text, kind, id Text) (protocol.JSONObject, error) {
	return c.SendMessage(ctx, chat, wecomJSONObject(wecomField("msgtype", kind), wecomField(kind.String(), wecomJSONObject(wecomField("media_id", id)))))
}

func wecomStrictZero(raw json.RawMessage) bool {
	var n float64
	return len(raw) > 0 && raw[0] != '"' && raw[0] != 'n' && json.Unmarshal(raw, &n) == nil && n == 0
}

func wecomWire(frame protocol.JSONObject) []byte {
	// JSON.parse credentials retain their actual value until this producer's
	// JSON.stringify boundary (overflow becomes null; numbers round to binary64).
	return protocol.StringifyValidatedJavaScriptJSON(raw(frame))
}
