package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

// The IDE WebSocket is an ordinary MCP peer with a distinct lockfile token.
// It does not consult OAuth or synthesize a successful connection snapshot.
type websocketTransport struct {
	mu              sync.Mutex
	config          Config
	options         Options
	conn            *websocket.Conn
	cancel          context.CancelFunc
	started, closed bool
	done            chan struct{}
	finish          sync.Once
}

func newWebSocketTransport(config Config, o Options) *websocketTransport {
	return &websocketTransport{config: config, options: o, done: make(chan struct{})}
}
func (t *websocketTransport) setProtocolVersion(string) {}
func (t *websocketTransport) start(ctx context.Context, receive func(rpcMessage), onError func(error), onClose func()) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("Start can only be called once per transport.")
	}
	t.started = true
	if t.closed {
		t.mu.Unlock()
		t.finish.Do(func() { close(t.done) })
		return context.Canceled
	}
	dialContext, dialCancel := context.WithCancel(ctx)
	t.cancel = dialCancel
	t.mu.Unlock()
	defer dialCancel()
	target, e := url.Parse(t.config.URL)
	if e != nil {
		t.finish.Do(func() { close(t.done) })
		return e
	}
	if target.Host == "" || (target.Scheme != "ws" && target.Scheme != "wss") {
		t.finish.Do(func() { close(t.done) })
		return errors.New("MCP WebSocket transport requires a WS(S) URL")
	}
	headers := http.Header{}
	headers.Set("User-Agent", t.options.UserAgent)
	if t.config.Type == "ws-ide" && t.config.AuthToken != "" {
		headers.Set("X-Claude-Code-Ide-Authorization", t.config.AuthToken)
	}
	// ws-ide's Source branch does not inherit the generic headers/OAuth path.
	if t.config.Type != "ws-ide" {
		if t.options.SessionIngressToken != nil {
			if token := t.options.SessionIngressToken(); token != "" {
				headers.Set("Authorization", "Bearer "+token)
			}
		}
		for k, v := range t.config.Headers {
			headers.Set(k, v)
		}
	}
	conn, _, e := websocket.Dial(dialContext, t.config.URL, &websocket.DialOptions{HTTPClient: t.options.HTTPClient, HTTPHeader: headers, Subprotocols: []string{"mcp"}})
	if e != nil {
		t.finish.Do(func() { close(t.done) })
		return e
	}
	conn.SetReadLimit(-1)
	// Client.closeState cancels pending RPCs. The transport still owns its
	// receiver until it closes and joins the native socket below.
	readCtx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	if t.closed || ctx.Err() != nil {
		t.mu.Unlock()
		cancel()
		_ = conn.CloseNow()
		t.finish.Do(func() { close(t.done) })
		return context.Canceled
	}
	t.conn, t.cancel = conn, cancel
	t.mu.Unlock()
	go func() {
		defer t.finish.Do(func() { close(t.done) })
		defer onClose()
		defer cancel()
		for {
			_, data, e := conn.Read(readCtx)
			if e != nil {
				if websocket.CloseStatus(e) == -1 && readCtx.Err() == nil {
					onError(errors.New("WebSocket error"))
				}
				return
			}
			message, e := decodeMessage(protocol.DecodeJavaScriptUTF8(data))
			if e != nil {
				onError(e)
				continue
			}
			receive(message)
		}
	}()
	return nil
}
func (t *websocketTransport) send(ctx context.Context, message rpcMessage) error {
	t.mu.Lock()
	conn, closed := t.conn, t.closed
	t.mu.Unlock()
	if closed || conn == nil {
		return errors.New("WebSocket is not open. Cannot send message.")
	}
	data, e := message.MarshalJSON()
	if e != nil {
		return e
	}
	return conn.Write(ctx, websocket.MessageText, protocol.StringifyValidatedJavaScriptJSON(data))
}
func (t *websocketTransport) close() error {
	t.mu.Lock()
	t.closed = true
	conn, cancel, started := t.conn, t.cancel, t.started
	t.mu.Unlock()
	var e error
	if conn != nil {
		e = conn.Close(websocket.StatusNormalClosure, "")
	}
	if cancel != nil {
		cancel()
	}
	if started {
		<-t.done
	}
	return e
}
