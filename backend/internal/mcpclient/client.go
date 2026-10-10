package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type reply struct {
	result protocol.JSONObject
	err    error
}
type pending struct {
	result   chan reply
	progress func(protocol.JSONObject)
}
type inbound struct{ cancel context.CancelFunc }

type Client struct {
	mu                        sync.Mutex
	options                   Options
	transport                 transport
	ctx                       context.Context
	cancel                    context.CancelFunc
	nextID                    int64
	pending                   map[string]pending
	inbound                   map[string]*inbound
	closed                    bool
	intentional               bool
	consecutiveErrors         int
	capabilities              protocol.JSONObject
	serverInfo                protocol.JSONObject
	instructions              json.RawMessage
	protocolVersion           string
	notificationID            uint64
	notificationSubscriptions []notificationSubscription
}

func Connect(ctx context.Context, config Config, options Options) (*Client, error) {
	config.Args = append([]string(nil), config.Args...)
	config.Env = copyStrings(config.Env)
	config.Headers = copyStrings(config.Headers)
	options.Environment = copyStrings(options.Environment)
	o := normalizeOptions(options)
	var t transport
	switch config.Type {
	case "", "stdio":
		t = newStdioTransport(config, o)
	case "sdk":
		if o.SDK == nil || o.SDK.Send == nil {
			return nil, errors.New("SDK MCP transport requires actual control-message binding")
		}
		t = &sdkControlTransport{bindings: o.SDK}
	case "http":
		t = newHTTPTransport(config, o, false)
	case "sse", "sse-ide":
		t = newHTTPTransport(config, o, true)
	default:
		return nil, fmt.Errorf("Unsupported server type: %s", config.Type)
	}
	root, cancel := context.WithCancel(context.Background())
	c := &Client{options: o, transport: t, ctx: root, cancel: cancel, pending: map[string]pending{}, inbound: map[string]*inbound{}}
	connection, stop := context.WithTimeout(ctx, o.ConnectTimeout)
	defer stop()
	started := make(chan error, 1)
	go func() { started <- t.start(root, c.receive, c.transportError, c.transportClosed) }()
	var startErr error
	select {
	case startErr = <-started:
	case <-connection.Done():
		startErr = connection.Err()
	}
	if startErr != nil {
		_ = c.Close()
		return nil, startErr
	}
	result, err := c.Request(connection, "initialize", object(field("protocolVersion", LatestProtocolVersion), field("capabilities", o.Capabilities), field("clientInfo", o.ClientInfo)), RequestOptions{})
	if err == nil {
		result, err = validateResult("initialize", result)
	}
	if err != nil {
		_ = c.Close()
		if errors.Is(connection.Err(), context.DeadlineExceeded) {
			return nil, &TransportError{Message: fmt.Sprintf("MCP server %q connection timed out after %dms", o.Name, o.ConnectTimeout.Milliseconds())}
		}
		return nil, err
	}
	version := result.String("protocolVersion")
	if !supportedProtocolVersions[version] {
		_ = c.Close()
		return nil, fmt.Errorf("Server's protocol version is not supported: %s", version)
	}
	c.mu.Lock()
	c.protocolVersion = version
	c.capabilities = nested(result.Get("capabilities"))
	c.serverInfo = nested(result.Get("serverInfo"))
	c.instructions = append(json.RawMessage(nil), result.Get("instructions")...)
	c.mu.Unlock()
	t.setProtocolVersion(version)
	if err = c.Notify(connection, "notifications/initialized", protocol.JSONObject{}); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
func copyStrings(source map[string]string) map[string]string {
	copy := map[string]string{}
	for k, v := range source {
		copy[k] = v
	}
	return copy
}

func nested(b json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = json.Unmarshal(b, &o)
	return o
}
func (c *Client) Capabilities() protocol.JSONObject {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.capabilities.Clone()
}
func (c *Client) ServerInfo() protocol.JSONObject {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverInfo.Clone()
}
func (c *Client) Instructions() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(json.RawMessage(nil), c.instructions...)
}
func (c *Client) ProtocolVersion() string { c.mu.Lock(); defer c.mu.Unlock(); return c.protocolVersion }
func (c *Client) SessionID() string {
	if t, ok := c.transport.(*httpTransport); ok {
		return t.sessionID()
	}
	return ""
}
func (c *Client) PID() int {
	if t, ok := c.transport.(*stdioTransport); ok {
		return t.pid()
	}
	return 0
}
func (c *Client) Stderr() string {
	if t, ok := c.transport.(*stdioTransport); ok {
		return t.stderrText()
	}
	return ""
}
func (c *Client) Done() <-chan struct{} { return c.ctx.Done() }

func (c *Client) Request(ctx context.Context, method string, params protocol.JSONObject, options RequestOptions) (protocol.JSONObject, error) {
	if err := ctx.Err(); err != nil {
		return protocol.JSONObject{}, abortError(options.CancelReason)
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return protocol.JSONObject{}, &RPCError{Code: -32000, Message: "Connection closed"}
	}
	id := c.nextID
	c.nextID++
	key := "n:" + strconv.FormatInt(id, 10)
	p := pending{result: make(chan reply, 1), progress: options.OnProgress}
	c.pending[key] = p
	c.mu.Unlock()
	if options.OnProgress != nil {
		params = params.Clone()
		meta := nested(params.Get("_meta"))
		if !meta.IsObject() {
			meta = object()
		}
		meta.Set("progressToken", raw(id))
		params.Set("_meta", raw(meta))
	}
	message := rpcMessage{JSONRPC: "2.0", ID: raw(id), Method: method, Params: params, hasMethod: true}
	go func() {
		if err := c.transport.send(c.ctx, message); err != nil {
			c.deliver(key, reply{err: err})
		}
	}()
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = c.options.RequestTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-p.result:
		return r.result, r.err
	case <-ctx.Done():
		err := abortError(options.CancelReason)
		c.cancelRequest(key, id, err.Message)
		return protocol.JSONObject{}, err
	case <-timer.C:
		err := &RPCError{Code: -32001, Message: "Request timed out", Data: raw(map[string]any{"timeout": timeout.Milliseconds()})}
		c.cancelRequest(key, id, "McpError: "+err.Error())
		return protocol.JSONObject{}, err
	case <-c.ctx.Done():
		return protocol.JSONObject{}, &RPCError{Code: -32000, Message: "Connection closed"}
	}
}
func abortError(reason string) *RPCError {
	if reason == "" {
		reason = "AbortError: This operation was aborted"
	}
	return &RPCError{Code: -32001, Message: reason}
}
func (c *Client) cancelRequest(key string, id int64, reason string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
	go func() {
		_ = c.Notify(c.ctx, "notifications/cancelled", object(field("requestId", id), field("reason", reason)))
	}()
}
func (c *Client) Notify(ctx context.Context, method string, params protocol.JSONObject) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return &RPCError{Code: -32000, Message: "Connection closed"}
	}
	return c.transport.send(ctx, rpcMessage{JSONRPC: "2.0", Method: method, Params: params, hasMethod: true})
}
func (c *Client) deliver(key string, r reply) {
	c.mu.Lock()
	p, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.mu.Unlock()
	if ok {
		p.result <- r
	}
}
func (c *Client) receive(m rpcMessage) {
	if !m.hasMethod && m.Method == "" {
		if len(m.ID) == 0 {
			return
		}
		key := idKey(m.ID)
		if m.Error != nil {
			c.deliver(key, reply{err: m.Error})
			return
		}
		result := nested(m.Result)
		if !result.IsObject() {
			c.deliver(key, reply{err: errors.New("MCP result must be an object")})
			return
		}
		c.deliver(key, reply{result: result})
		return
	}
	if len(m.ID) > 0 {
		c.handleRequest(m)
		return
	}
	if m.Method == "notifications/progress" {
		key := progressKey(m.Params.Get("progressToken"))
		c.mu.Lock()
		p, ok := c.pending[key]
		c.mu.Unlock()
		if ok && p.progress != nil {
			data := m.Params.Clone()
			data.Delete("progressToken")
			p.progress(data)
		}
	}
	if m.Method == "notifications/cancelled" {
		c.mu.Lock()
		r, ok := c.inbound[idKey(m.Params.Get("requestId"))]
		c.mu.Unlock()
		if ok {
			r.cancel()
		}
	}
	if c.options.OnNotification != nil {
		c.options.OnNotification(m.Method, m.Params)
	}
	c.dispatchNotificationObservers(m.Method, m.Params)
}
func idKey(id json.RawMessage) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		return "s:" + string(protocol.StringifyValidatedJavaScriptJSON(id))
	}
	var n json.Number
	if json.Unmarshal(id, &n) == nil {
		if f, err := n.Float64(); err == nil {
			return "n:" + strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	return string(id)
}
func progressKey(id json.RawMessage) string {
	var s string
	if json.Unmarshal(id, &s) == nil {
		if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return "n:" + strconv.FormatFloat(n, 'f', -1, 64)
		}
		if strings.TrimSpace(s) == "" {
			return "n:0"
		}
		return "invalid"
	}
	return idKey(id)
}
func (c *Client) handleRequest(m rpcMessage) {
	ctx, cancel := context.WithCancel(c.ctx)
	key := idKey(m.ID)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return
	}
	owned := &inbound{cancel}
	c.inbound[key] = owned
	c.mu.Unlock()
	// Register synchronously before the next wire message can cancel it. The
	// handler runs asynchronously, as the original Protocol request dispatcher.
	go c.executeRequest(ctx, cancel, owned, key, m)
}
func (c *Client) executeRequest(ctx context.Context, cancel context.CancelFunc, owned *inbound, key string, m rpcMessage) {
	defer cancel()
	defer func() {
		c.mu.Lock()
		if c.inbound[key] == owned {
			delete(c.inbound, key)
		}
		c.mu.Unlock()
	}()
	var result protocol.JSONObject
	var err error
	if c.options.OnRequest != nil {
		result, err = c.options.OnRequest(ctx, m.Method, m.Params)
	} else {
		switch m.Method {
		case "ping":
			result = object()
		case "roots/list":
			result = object(field("roots", []any{object(field("uri", c.options.RootURI))}))
		case "elicitation/create":
			if c.ProtocolVersion() == "" {
				err = &RPCError{Code: -32601, Message: "Method not found"}
			} else {
				result = object(field("action", "cancel"))
			}
		default:
			err = &RPCError{Code: -32601, Message: "Method not found"}
		}
	}
	if ctx.Err() != nil {
		return
	}
	response := rpcMessage{JSONRPC: "2.0", ID: m.ID, Result: raw(result)}
	if err != nil {
		var rpc *RPCError
		if !errors.As(err, &rpc) {
			rpc = &RPCError{Code: -32603, Message: err.Error()}
		}
		response.Result = nil
		response.Error = rpc
	}
	if err = c.transport.send(c.ctx, response); err != nil {
		c.transportError(err)
	}
}
func (c *Client) transportError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	terminal := false
	for _, s := range []string{"ECONNRESET", "ETIMEDOUT", "EPIPE", "EHOSTUNREACH", "ECONNREFUSED", "Body Timeout Error", "terminated", "SSE stream disconnected", "Failed to reconnect SSE stream"} {
		if strings.Contains(err.Error(), s) {
			terminal = true
		}
	}
	if terminal {
		c.consecutiveErrors++
	} else {
		c.consecutiveErrors = 0
	}
	closeNeeded := c.consecutiveErrors >= 3 || strings.Contains(err.Error(), "Maximum reconnection attempts") || IsSessionExpired(err)
	c.mu.Unlock()
	if c.options.OnError != nil {
		c.options.OnError(err)
	}
	if closeNeeded {
		c.closeState()
		go c.transport.close()
	}
}
func IsSessionExpired(err error) bool {
	var e *HTTPError
	return errors.As(err, &e) && e.Status == 404 && (strings.Contains(e.Message, `"code":-32001`) || strings.Contains(e.Message, `"code": -32001`))
}
func (c *Client) transportClosed() { c.closeState() }
func (c *Client) closeState() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.notificationSubscriptions = nil
	requests := c.pending
	c.pending = map[string]pending{}
	for _, r := range c.inbound {
		r.cancel()
	}
	c.inbound = map[string]*inbound{}
	intentional := c.intentional
	c.mu.Unlock()
	c.cancel()
	for _, p := range requests {
		p.result <- reply{err: &RPCError{Code: -32000, Message: "Connection closed"}}
	}
	if c.options.OnClose != nil {
		c.options.OnClose(intentional)
	}
}
func (c *Client) shutdown(intentional bool) error {
	c.mu.Lock()
	if intentional {
		c.intentional = true
	}
	c.mu.Unlock()
	c.closeState()
	return c.transport.close()
}
func (c *Client) Close() error { return c.shutdown(true) }

// Close only aborts local streams, exactly as the original Client.cleanup.
// Explicit server-session termination is separately available to its owner.
func (c *Client) TerminateSession(ctx context.Context) error {
	if t, ok := c.transport.(*httpTransport); ok && !t.legacy {
		return t.terminateSession(ctx)
	}
	return nil
}

func decodeMessage(b []byte) (rpcMessage, error) {
	var m rpcMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.JSONRPC != "2.0" {
		return m, errors.New("Invalid JSON-RPC version")
	}
	o := nested(b)
	m.hasMethod = o.Get("method") != nil
	if m.hasMethod && !hasType(o.Get("method"), "string") {
		return m, errors.New("Invalid JSON-RPC method")
	}
	if len(m.ID) > 0 {
		id := bytes.TrimSpace(m.ID)
		var n json.Number
		if len(id) == 0 || (id[0] != '"' && (json.Unmarshal(id, &n) != nil || !integerNumber(n))) {
			return m, errors.New("Invalid JSON-RPC request id")
		}
	}
	if !m.hasMethod && m.Method == "" && (len(m.Result) > 0 && len(m.ID) == 0 || len(m.Result) == 0 && m.Error == nil) {
		return m, errors.New("Invalid JSON-RPC message")
	}
	if m.hasMethod && (len(m.Result) > 0 || m.Error != nil) || len(m.Result) > 0 && m.Error != nil {
		return m, errors.New("Invalid JSON-RPC message fields")
	}
	for _, f := range fields(o) {
		if f.Name != "jsonrpc" && f.Name != "id" && f.Name != "method" && f.Name != "params" && f.Name != "result" && f.Name != "error" {
			return m, errors.New("Unrecognized JSON-RPC field: " + f.Name)
		}
	}
	if len(m.Result) > 0 && !nested(m.Result).IsObject() {
		return m, errors.New("Invalid JSON-RPC result")
	}
	if v := o.Get("params"); v != nil && (!m.hasMethod || !hasType(v, "object")) {
		return m, errors.New("Invalid JSON-RPC params")
	}
	return m, nil
}
func integerNumber(n json.Number) bool {
	f, e := n.Float64()
	return e == nil && math.Trunc(f) == f && math.Abs(f) <= 9007199254740991
}
