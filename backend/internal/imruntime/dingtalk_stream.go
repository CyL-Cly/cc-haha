// DingTalk Stream SDK2.1.4 protocol port.
// MIT License, Copyright (c) 2023 钉钉开放平台团队
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.
package imruntime

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type DingTalkStreamOptions struct {
	API  *DingTalkAPI
	TLS  *tls.Config
	Dial func(context.Context, string, *tls.Config) (*Socket, error)
	// Admission executes the synchronous ACK/dedup prefix; Dispatcher schedules business.
	OnRobot, OnCard                      func(context.Context, protocol.JSONObject) error
	OnError                              func(error)
	OnConnected                          func()
	OnFrame                              func(protocol.JSONObject)
	HeartbeatInterval, ReconnectInterval time.Duration
}
type DingTalkStream struct {
	o                                                  DingTalkStreamOptions
	ctx                                                context.Context
	cancel                                             context.CancelFunc
	mu                                                 sync.Mutex
	socket                                             *Socket
	closed, connected, registered, reconnecting, alive bool
	failure                                            error
	wg                                                 sync.WaitGroup
	start, close                                       sync.Once
	done                                               chan struct{}
}

func NewDingTalkStream(ctx context.Context, o DingTalkStreamOptions) (*DingTalkStream, error) {
	if o.API == nil {
		return nil, errors.New("DingTalk stream requires actual API producer")
	}
	if !credentialTruthy(o.API.o.Credentials.Get("clientId")) || !credentialTruthy(o.API.o.Credentials.Get("clientSecret")) {
		return nil, errors.New("clientId or clientSecret is null")
	}

	if o.HeartbeatInterval == 0 {
		o.HeartbeatInterval = 8 * time.Second
	}
	if o.ReconnectInterval == 0 {
		o.ReconnectInterval = time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	return &DingTalkStream{o: o, ctx: ctx, cancel: cancel, done: make(chan struct{})}, nil
}
func (c *DingTalkStream) emit(e error) {
	if c.o.OnError != nil {
		c.o.OnError(e)
	}
}

// Source connect resolves after gateway allocation and WebSocket construction,
// before open/REGISTERED. Dial/stream failures are asynchronous error events.
func (c *DingTalkStream) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return context.Canceled
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	// Closing the stream also cancels its initial gateway allocation, while
	// the borrowed API remains independently owned by the adapter runtime.
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer func() { stop(); cancel() }()
	address, _, e := c.o.API.Gateway(ctx)
	if e != nil {
		return e
	}
	u, e := url.Parse(address)
	if e != nil {
		return e
	}
	if u.Scheme != "wss" && u.Scheme != "ws" {
		return errors.New("invalid WebSocket URL")
	}
	c.start.Do(func() {
		c.mu.Lock()
		if c.closed {
			close(c.done)
			c.mu.Unlock()
			return
		}
		c.wg.Add(1)
		c.mu.Unlock()
		go func() { defer c.wg.Done(); defer close(c.done); c.run(address) }()
	})
	return nil
}
func (c *DingTalkStream) Wait() error { <-c.done; c.mu.Lock(); defer c.mu.Unlock(); return c.failure }
func (c *DingTalkStream) Close() {
	c.close.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		socket := c.socket
		c.socket = nil
		c.mu.Unlock()
		if socket != nil {
			_ = socket.conn.Close()
		}
		c.start.Do(func() { close(c.done) })
		c.wg.Wait()
	})
}
func (c *DingTalkStream) Snapshot() protocol.JSONObject {
	c.mu.Lock()
	defer c.mu.Unlock()
	return protocol.NewJSONObject(dingTalkField("connected", c.connected), dingTalkField("registered", c.registered), dingTalkField("reconnecting", c.reconnecting), dingTalkField("isAlive", c.alive))
}
func (c *DingTalkStream) send(value protocol.JSONObject) error {
	c.mu.Lock()
	socket := c.socket
	c.mu.Unlock()
	if socket == nil {
		return nil
	}
	return socket.Send(dingTalkWire(value))
}
func (c *DingTalkStream) CallbackACK(messageID json.RawMessage, result protocol.JSONObject) error {
	if !credentialTruthy(messageID) {
		return errors.New("send: messageId must be defined")
	}
	data := protocol.NewJSONObject(dingTalkField("response", result))
	return c.send(protocol.NewJSONObject(dingTalkField("code", 200), dingTalkField("headers", protocol.NewJSONObject(dingTalkField("contentType", "application/json"), protocol.JSONField{Name: "messageId", Value: messageID})), dingTalkField("message", "OK"), dingTalkField("data", NewText(string(dingTalkWire(data))))))
}
func (c *DingTalkStream) run(address string) {
	for c.ctx.Err() == nil {
		var socket *Socket
		var decoder *qqGatewayNativeSocket
		var e error
		if c.o.Dial != nil {
			socket, e = c.o.Dial(c.ctx, address, c.o.TLS)
		} else {
			// Reuse the existing pure native WebSocket handshake/deflate negotiation;
			// DingTalk observes pong itself and never calls the QQ business client.
			var physical QQGatewaySocket
			physical, e = DialQQGatewaySocket(c.ctx, address, make(http.Header), c.o.TLS)
			if e == nil {
				decoder = physical.(*qqGatewayNativeSocket)
				socket = decoder.socket
			}
		}
		if e != nil {
			if c.ctx.Err() == nil {
				c.emit(e)
			}
		} else {
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				_ = socket.conn.Close()
				return
			}
			c.socket = socket
			c.connected = true
			c.alive = true
			c.wg.Add(1)
			c.mu.Unlock()
			// SDK2.1.4 leaves earlier keepAlive intervals installed on reconnect;
			// each points at the current socket. The process-owned Close cancels
			// and joins all of them, as the original adapter's process.exit does.
			go c.heartbeat()
			if c.o.OnConnected != nil {
				c.o.OnConnected()
			}
			for c.ctx.Err() == nil {
				data, e := c.readCompressed(socket, decoder)
				if e != nil {
					break
				}
				var frame protocol.JSONObject
				if e = frame.UnmarshalJSON(protocol.DecodeJavaScriptUTF8(data)); e != nil {
					// An invalid JSON frame throws out of Source's message listener.
					// Propagate a terminal error rather than processing later messages.
					c.mu.Lock()
					c.failure = e
					c.mu.Unlock()
					c.cancel()
					c.emit(e)
					break
				}
				if c.o.OnFrame != nil {
					c.o.OnFrame(frame.Clone())
				}
				if e = c.handle(frame); e != nil {
					c.emit(e)
				}
			}
			_ = socket.conn.Close()
			c.mu.Lock()
			if c.socket == socket {
				c.socket = nil
			}
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.connected = false
		c.registered = false
		c.reconnecting = c.ctx.Err() == nil
		c.mu.Unlock()
		if c.ctx.Err() != nil {
			return
		}
		if e := dingTalkWait(c.ctx, float64(c.o.ReconnectInterval)/float64(time.Millisecond)); e != nil {
			return
		}
		address, _, e = c.o.API.Gateway(c.ctx)
		if e != nil {
			c.mu.Lock()
			c.failure = e
			c.mu.Unlock()
			c.emit(e)
			return
		}
	}
}
func (c *DingTalkStream) heartbeat() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.o.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			alive, socket := c.alive, c.socket
			if alive {
				c.alive = false
			}
			c.mu.Unlock()
			if socket == nil {
				continue
			}
			if !alive {
				_ = socket.conn.Close()
			} else {
				if e := socket.frame(9, []byte{}); e != nil && c.ctx.Err() == nil {
					c.emit(e)
				}
			}
		}
	}
}
func (c *DingTalkStream) handle(frame protocol.JSONObject) error {
	headers := object(frame.Get("headers"))
	switch frame.String("type") {
	case "SYSTEM":
		switch headers.String("topic") {
		case "REGISTERED":
			c.mu.Lock()
			c.registered = true
			c.reconnecting = false
			c.mu.Unlock()
		case "disconnect":
			c.mu.Lock()
			c.connected = false
			c.registered = false
			c.mu.Unlock()
		case "KEEPALIVE":
			c.mu.Lock()
			c.alive = true
			c.mu.Unlock()
		case "ping":
			return c.send(protocol.NewJSONObject(dingTalkField("code", 200), protocol.JSONField{Name: "headers", Value: frame.Get("headers")}, dingTalkField("message", "OK"), protocol.JSONField{Name: "data", Value: frame.Get("data")}))
		}
	case "EVENT":
		return c.send(protocol.NewJSONObject(dingTalkField("code", 200), dingTalkField("headers", protocol.NewJSONObject(dingTalkField("contentType", "application/json"), protocol.JSONField{Name: "messageId", Value: headers.Get("messageId")})), dingTalkField("message", "OK"), dingTalkField("data", `{"status":"SUCCESS"}`)))
	case "CALLBACK":
		var callback func(context.Context, protocol.JSONObject) error
		switch headers.String("topic") {
		case DingTalkRobotTopic:
			callback = c.o.OnRobot
		case DingTalkCardTopic:
			callback = c.o.OnCard
		}
		if callback != nil {
			return callback(c.ctx, frame.Clone())
		}
	}
	return nil
}

func (c *DingTalkStream) schedule(callback func(context.Context) error) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return context.Canceled
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		if e := callback(c.ctx); e != nil && c.ctx.Err() == nil {
			c.emit(e)
		}
	}()
	return nil
}

// The shared Socket.Read consumes pong internally. DingTalk needs the native
// control-frame observation to update its isAlive state, so this reader reuses
// the same connection/buffer/write primitive while exposing that one signal.
func (c *DingTalkStream) read(socket *Socket) ([]byte, error) { return c.readCompressed(socket, nil) }
func (c *DingTalkStream) readCompressed(socket *Socket, decoder *qqGatewayNativeSocket) ([]byte, error) {
	result := []byte{}
	compressed, fragmented := false, false
	for {
		var head [2]byte
		if _, e := io.ReadFull(socket.read, head[:]); e != nil {
			return nil, e
		}
		op, final := head[0]&15, head[0]&0x80 != 0
		if head[0]&0x30 != 0 {
			return nil, errors.New("Invalid WebSocket RSV2/RSV3 flag")
		}
		if head[0]&0x40 != 0 {
			if decoder == nil || !decoder.compressed || op == 0 || op >= 8 {
				return nil, errors.New("Invalid WebSocket RSV1 flag")
			}
			compressed = true
		}
		length := uint64(head[1] & 127)
		if length == 126 {
			var size [2]byte
			if _, e := io.ReadFull(socket.read, size[:]); e != nil {
				return nil, e
			}
			length = uint64(binary.BigEndian.Uint16(size[:]))
		} else if length == 127 {
			var size [8]byte
			if _, e := io.ReadFull(socket.read, size[:]); e != nil {
				return nil, e
			}
			length = binary.BigEndian.Uint64(size[:])
		}
		if length > 100*1024*1024 {
			return nil, errors.New("Max payload size exceeded")
		}
		if head[1]&0x80 != 0 {
			return nil, errors.New("Invalid server WebSocket masked frame")
		}
		if op >= 8 && (!final || length > 125) {
			return nil, errors.New("Invalid WebSocket control frame")
		}
		payload := make([]byte, int(length))
		if _, e := io.ReadFull(socket.read, payload); e != nil {
			return nil, e
		}

		switch op {
		case 8:
			code := 1005
			reason := ""
			if len(payload) >= 2 {
				code = int(binary.BigEndian.Uint16(payload[:2]))
				reason = string(protocol.DecodeJavaScriptUTF8(payload[2:]))
			}
			_ = socket.frame(8, payload)
			return nil, &SocketClose{code, reason}
		case 9:
			_ = socket.frame(10, payload)
			continue
		case 10:
			c.mu.Lock()
			c.alive = true
			c.mu.Unlock()
			continue
		case 0:
			if !fragmented {
				return nil, errors.New("Unexpected WebSocket continuation")
			}
			result = append(result, payload...)
		case 1, 2:
			if fragmented {
				return nil, errors.New("Invalid WebSocket fragmented message")
			}
			fragmented = !final
			result = append(result, payload...)
		default:
			return nil, fmt.Errorf("invalid WebSocket opcode")
		}
		if !final {
			fragmented = true
			continue
		}
		if !compressed {
			return result, nil
		}
		encoded := append(append([]byte{}, result...), 0, 0, 255, 255, 1, 0, 0, 255, 255)
		reader := flate.NewReaderDict(bytes.NewReader(encoded), decoder.dictionary)
		decoded, e := io.ReadAll(io.LimitReader(reader, 100*1024*1024+1))
		reader.Close()
		if e != nil {
			return nil, e
		}
		if len(decoded) > 100*1024*1024 {
			return nil, errors.New("Max payload size exceeded")
		}
		if !decoder.noContextTakeover {
			decoder.dictionary = append(decoder.dictionary, decoded...)
			if len(decoder.dictionary) > 32768 {
				decoder.dictionary = append([]byte{}, decoder.dictionary[len(decoder.dictionary)-32768:]...)
			}
		}
		return decoded, nil
	}
}

// The original index ACKs before header/body dedup and before async business.
// The consumer owns routing/authorization; this producer never creates an Agent.
type DingTalkDispatcher struct {
	Stream *DingTalkStream
	Dedup  *MessageDedup
	// An admission producer performs Source's synchronous prefix before its
	// first await. Only its returned continuation runs asynchronously.
	RobotAdmission, CardAdmission func(context.Context, json.RawMessage) (func(context.Context) error, error)
	Robot                         func(context.Context, json.RawMessage) error
	Card                          func(context.Context, json.RawMessage) error
}

func (d *DingTalkDispatcher) HandleRobot(ctx context.Context, frame protocol.JSONObject) error {
	messageID := object(frame.Get("headers")).Get("messageId")
	if credentialTruthy(messageID) {
		if e := d.Stream.CallbackACK(messageID, protocol.NewJSONObject(dingTalkField("success", true))); e != nil {
			return e
		}
		if !d.Dedup.TryRecord("header:" + wecomKey(dingTalkCoerce(messageID))) {
			return nil
		}
	}
	data := ParseDingTalkPayload(frame.Get("data"))
	if data == nil {
		return nil
	}
	bodyID := object(data).Get("msgId")
	if credentialTruthy(bodyID) && !d.Dedup.TryRecord("body:"+wecomKey(dingTalkCoerce(bodyID))) {
		return nil
	}
	if d.RobotAdmission != nil {
		continuation, e := d.RobotAdmission(ctx, data)
		if e != nil {
			return e
		}
		if continuation != nil {
			return d.Stream.schedule(continuation)
		}
		return nil
	}
	if d.Robot != nil {
		return d.Stream.schedule(func(ctx context.Context) error { return d.Robot(ctx, data) })
	}
	return nil
}
func (d *DingTalkDispatcher) HandleCard(ctx context.Context, frame protocol.JSONObject) error {
	messageID := object(frame.Get("headers")).Get("messageId")
	if credentialTruthy(messageID) {
		if e := d.Stream.CallbackACK(messageID, protocol.NewJSONObject(dingTalkField("success", true))); e != nil {
			return e
		}
		if !d.Dedup.TryRecord("card:" + wecomKey(dingTalkCoerce(messageID))) {
			return nil
		}
	}
	value := frame.Get("data")
	if len(value) == 0 || string(value) == "null" {
		value = raw(frame)
	}
	if d.CardAdmission != nil {
		continuation, e := d.CardAdmission(ctx, value)
		if e != nil {
			return e
		}
		if continuation != nil {
			return d.Stream.schedule(continuation)
		}
		return nil
	}
	if d.Card != nil {
		return d.Stream.schedule(func(ctx context.Context) error { return d.Card(ctx, value) })
	}
	return nil
}
