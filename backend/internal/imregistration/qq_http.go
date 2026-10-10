package imregistration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

const QQRequestIdleTimeout = 10 * time.Second

type qqIdleConn struct {
	net.Conn
	mu              sync.Mutex
	responseStarted bool
}

func (c *qqIdleConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	if !c.responseStarted {
		_ = c.Conn.SetReadDeadline(time.Now().Add(QQRequestIdleTimeout))
	} else {
		_ = c.Conn.SetReadDeadline(time.Time{})
	}
	c.mu.Unlock()
	n, e := c.Conn.Read(p)

	return n, e
}
func (c *qqIdleConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(QQRequestIdleTimeout))
	n, e := c.Conn.Write(p)

	return n, e
}

func (c *qqIdleConn) requestStarted() {
	c.mu.Lock()
	c.responseStarted = false
	// A reused keep-alive socket may already be waiting in the read loop with
	// the previous response's cleared deadline. Arm this request immediately.
	_ = c.Conn.SetReadDeadline(time.Now().Add(QQRequestIdleTimeout))
	c.mu.Unlock()
}
func (c *qqIdleConn) headersReceived() {
	c.mu.Lock()
	c.responseStarted = true
	_ = c.Conn.SetReadDeadline(time.Time{})
	c.mu.Unlock()
}
func qqSocket(conn net.Conn) *qqIdleConn {
	for conn != nil {
		if native, ok := conn.(*qqIdleConn); ok {
			return native
		}
		if tls, ok := conn.(interface{ NetConn() net.Conn }); ok {
			conn = tls.NetConn()
		} else {
			return nil
		}
	}
	return nil
}

// Node's vendor HTTPS request has a socket inactivity timeout, not fetch's
// total deadline, and it never follows redirect responses. The cloned native
// transport retains the supplied TLS/proxy policy and owns this connector's
// sockets. A custom business RoundTripper cannot stand in for that primitive.
func newQQHTTPClient(client *http.Client) (*http.Client, *http.Transport, error) {
	if client == nil {
		return nil, nil, fmt.Errorf("QQ registration requires actual HTTP client")
	}
	var transport *http.Transport
	if client.Transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	} else if original, ok := client.Transport.(*http.Transport); ok {
		transport = original.Clone()
	} else {
		return nil, nil, fmt.Errorf("QQ registration requires native HTTP socket transport")
	}
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := dial(ctx, network, address)
		if e != nil {
			return nil, e
		}
		return &qqIdleConn{Conn: c}, nil
	}
	if dialTLS := transport.DialTLSContext; dialTLS != nil {
		transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			c, e := dialTLS(ctx, network, address)
			if e != nil {
				return nil, e
			}
			return &qqIdleConn{Conn: c}, nil
		}
	}
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = nil
	transport.DisableCompression = true
	copy := *client
	copy.Transport, copy.Timeout = transport, 0
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy, transport, nil
}

type qqHTTP struct {
	client    *http.Client
	transport *http.Transport
	drains    *sync.WaitGroup
}

func qqHTTPTimeout(e error) bool {
	var timeout net.Error
	return errors.As(e, &timeout) && timeout.Timeout()
}

func (h *qqHTTP) post(ctx context.Context, target string, body protocol.JSONObject) (protocol.JSONObject, error) {
	raw, e := body.MarshalJSON()
	if e != nil {
		return record(), e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if e != nil {
		return record(), e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// The installed vendor supplies no explicit User-Agent, but the original
	// pinned Bun https primitive adds this header on the actual wire.
	req.Header.Set("User-Agent", "Bun/1.3.14")
	req.Header.Set("Connection", "keep-alive")
	var socket *qqIdleConn
	var socketMu sync.Mutex
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		socketMu.Lock()
		socket = qqSocket(info.Conn)
		if socket != nil {
			socket.requestStarted()
		}
		socketMu.Unlock()
	}}))
	res, e := h.client.Do(req)
	if e != nil {
		if qqHTTPTimeout(e) {
			return record(), fmt.Errorf("timeout fetching %s", target)
		}
		return record(), e
	}
	// Actual pinned Bun's https ClientRequest timeout stops at the response
	// headers. The vendor's partial response promise remains pending; only
	// service shutdown cancels its body. Do not impose a new post-header timer.
	socketMu.Lock()
	if socket != nil {
		socket.headersReceived()
	}
	socketMu.Unlock()
	if res.StatusCode != http.StatusOK {
		// Vendor rejects as soon as headers arrive and calls response.resume().
		// Draining belongs to the same service's HTTP lifecycle; it must not
		// delay the caller or be abandoned when the service closes.
		h.drains.Add(1)
		go func() { defer h.drains.Done(); defer res.Body.Close(); _, _ = io.Copy(io.Discard, res.Body) }()
		return record(), fmt.Errorf("HTTP %d from %s", res.StatusCode, target)
	}
	defer res.Body.Close()
	// Vendor += each data Buffer converts UTF8 separately. Joining byte chunks
	// before decoding would repair a split code point that Source replaces.
	var decodedBody bytes.Buffer
	buffer := make([]byte, 32*1024)
	for {
		n, readError := res.Body.Read(buffer)
		if n > 0 {
			decodedBody.Write(protocol.DecodeJavaScriptUTF8(buffer[:n]))
		}
		if readError == io.EOF {
			break
		}
		if readError != nil {
			if qqHTTPTimeout(readError) {
				return record(), fmt.Errorf("timeout fetching %s", target)
			}
			return record(), readError
		}
	}
	decoded := decodedBody.Bytes()
	if message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(string(decoded)); invalid {
		text, e := feishuString(message)
		if e != nil {
			return record(), e
		}
		return record(), fmt.Errorf("%s", text)
	}
	trimmed := bytes.TrimSpace(decoded)
	if bytes.Equal(trimmed, []byte("null")) {
		return record(), fmt.Errorf("null is not an object (evaluating 'e.retcode')")
	}
	// JSON.parse accepts primitives and arrays. Their retcode property is
	// undefined, so the vendor's next stage returns its ordinary failure.
	var value protocol.JSONObject
	if len(trimmed) != 0 && trimmed[0] == '{' {
		if e = value.UnmarshalJSON(trimmed); e != nil {
			return record(), e
		}
	} else {
		value = record()
	}
	return value, nil
}

func qqCreateResult(value protocol.JSONObject) (json.RawMessage, error) {
	if !qqZero(value.Get("retcode")) {
		message := value.Get("msg")
		if len(message) == 0 || string(message) == "null" {
			return nil, fmt.Errorf("create_bind_task failed")
		}
		text, e := feishuString(message)
		if e != nil {
			return nil, e
		}
		return nil, fmt.Errorf("%s", text)
	}
	task := feishuProperty(value.Get("data"), "task_id")
	if !truthy(task) {
		return nil, fmt.Errorf("create_bind_task: missing task_id")
	}
	return append(json.RawMessage(nil), task...), nil
}

func qqPollResult(value protocol.JSONObject) (protocol.JSONObject, error) {
	if !qqZero(value.Get("retcode")) {
		message := value.Get("msg")
		if len(message) == 0 || string(message) == "null" {
			return record(), fmt.Errorf("poll_bind_result failed")
		}
		text, e := feishuString(message)
		if e != nil {
			return record(), e
		}
		return record(), fmt.Errorf("%s", text)
	}
	data := value.Get("data")
	status := feishuProperty(data, "status")
	if len(status) == 0 || string(status) == "null" {
		status = encoded(0)
	}
	id := feishuProperty(data, "bot_appid")
	if len(id) == 0 || string(id) == "null" {
		id = encoded("")
	}
	idString, e := protocol.ExactJavaScriptStringJSON(id)
	if e != nil {
		return record(), e
	}
	secret := feishuProperty(data, "bot_encrypt_secret")
	if len(secret) == 0 || string(secret) == "null" {
		secret = encoded("")
	}
	user := feishuProperty(data, "user_openid")
	if !truthy(user) {
		user = nil
	}
	return record(protocol.JSONField{Name: "status", Value: status}, protocol.JSONField{Name: "botAppId", Value: idString}, protocol.JSONField{Name: "botEncryptSecret", Value: secret}, protocol.JSONField{Name: "userOpenid", Value: user}), nil
}
