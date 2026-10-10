package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type httpTransport struct {
	mu            sync.Mutex
	config        Config
	options       Options
	legacy        bool
	ctx           context.Context
	cancel        context.CancelFunc
	endpoint      *url.URL
	url           *url.URL
	session       string
	version       string
	retry         time.Duration
	receive       func(rpcMessage)
	onError       func(error)
	onClose       func()
	endpointReady chan error
	readyOnce     sync.Once
	closeOnce     sync.Once
	closed        bool
}

func newHTTPTransport(config Config, options Options, legacy bool) *httpTransport {
	return &httpTransport{config: config, options: options, legacy: legacy, endpointReady: make(chan error, 1)}
}
func (t *httpTransport) start(ctx context.Context, receive func(rpcMessage), onError func(error), onClose func()) error {
	u, err := url.Parse(t.config.URL)
	if err != nil {
		return err
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("MCP transport requires an HTTP(S) URL")
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return context.Canceled
	}
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.url = u
	t.receive = receive
	t.onError = onError
	t.onClose = onClose
	t.mu.Unlock()
	if !t.legacy {
		return nil
	}
	go t.openStream("", true, 0)
	select {
	case err := <-t.endpointReady:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (t *httpTransport) headers(ctx context.Context) (http.Header, error) {
	h := http.Header{}
	if t.options.Auth != nil {
		token, err := t.options.Auth.Token(ctx)
		if err != nil {
			return nil, err
		}
		if token != "" {
			h.Set("Authorization", "Bearer "+token)
		}
	}
	t.mu.Lock()
	session, version := t.session, t.version
	t.mu.Unlock()
	if !t.legacy && session != "" {
		h.Set("Mcp-Session-Id", session)
	}
	if version != "" {
		h.Set("Mcp-Protocol-Version", version)
	}
	h.Set("User-Agent", t.options.UserAgent)
	for k, v := range t.config.Headers {
		h.Set(k, v)
	}
	return h, nil
}

// The original timeout covers fetch until response headers arrive. SSE bodies
// have the connection lifetime, so a timer cannot become stale after connect.
func (t *httpTransport) fetch(ctx context.Context, method string, u *url.URL, body []byte, lastID string) (*http.Response, error) {
	h, err := t.headers(ctx)
	if err != nil {
		return nil, err
	}
	if method == "GET" {
		h.Set("Accept", "text/event-stream")
		if lastID != "" {
			h.Set("Last-Event-ID", lastID)
		}
	} else {
		if h.Get("Accept") == "" || method == "POST" && !t.legacy {
			h.Set("Accept", "application/json, text/event-stream")
		}
		if method == "POST" {
			h.Set("Content-Type", "application/json")
		}
	}
	requestContext, cancel := context.WithCancel(ctx)
	var timer *time.Timer
	if method != "GET" {
		timer = time.AfterFunc(t.options.HTTPTimeout, cancel)
	}
	request, err := http.NewRequestWithContext(requestContext, method, u.String(), bytes.NewReader(body))
	if err != nil {
		if timer != nil {
			timer.Stop()
		}
		cancel()
		return nil, err
	}
	request.Header = h
	response, err := t.options.HTTPClient.Do(request)
	if timer != nil {
		timer.Stop()
	}
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &cancelBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { err := b.ReadCloser.Close(); b.cancel(); return err }
func (t *httpTransport) send(ctx context.Context, message rpcMessage) error {
	t.mu.Lock()
	target := t.url
	if t.legacy {
		target = t.endpoint
	}
	t.mu.Unlock()
	if target == nil {
		return errors.New("Not connected")
	}
	response, err := t.fetchAuthorized(ctx, "POST", target, raw(message), "")
	if err != nil {
		t.report(err)
		return err
	}
	if session := response.Header.Get("Mcp-Session-Id"); session != "" && !t.legacy {
		t.mu.Lock()
		t.session = session
		t.mu.Unlock()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		b, _ := io.ReadAll(response.Body)
		if t.legacy {
			err = &TransportError{Message: fmt.Sprintf("Error POSTing to endpoint (HTTP %d): %s", response.StatusCode, b)}
		} else {
			err = &HTTPError{Status: response.StatusCode, Message: "Error POSTing to endpoint: " + string(b)}
		}
		t.report(err)
		return err
	}
	if t.legacy {
		_ = response.Body.Close()
		return nil
	}
	if response.StatusCode == 202 {
		_ = response.Body.Close()
		if message.Method == "notifications/initialized" {
			go t.openStream("", false, 0)
		}
		return nil
	}
	if message.Method == "" || len(message.ID) == 0 {
		_ = response.Body.Close()
		return nil
	}
	contentType := response.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		go t.consumeStream(response.Body, "", false, 0)
		return nil
	}
	defer response.Body.Close()
	if !strings.Contains(contentType, "application/json") {
		err = &HTTPError{Status: -1, Message: "Unexpected content type: " + contentType}
		t.report(err)
		return err
	}
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.report(err)
		return err
	}
	if err = t.decodeMessages(b); err != nil {
		t.report(err)
	}
	return err
}
func (t *httpTransport) fetchAuthorized(ctx context.Context, method string, target *url.URL, body []byte, lastID string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		response, err := t.fetch(ctx, method, target, body, lastID)
		if err != nil {
			return nil, err
		}
		status := response.StatusCode
		upscope := status == 403 && strings.Contains(response.Header.Get("WWW-Authenticate"), "insufficient_scope")
		if status != 401 && !upscope {
			return response, nil
		}
		challenge := AuthChallenge{URL: t.config.URL, Status: status, WWWAuthenticate: response.Header.Get("WWW-Authenticate")}
		if t.options.Auth == nil {
			// No configured auth does not launch browser or inspect token stores.
			if status == 401 {
				_ = response.Body.Close()
				return nil, &AuthError{challenge}
			}
			return response, nil
		}
		_ = response.Body.Close()
		if attempt >= 1 {
			if status == 401 {
				return nil, &HTTPError{Status: 401, Message: "Server returned 401 after successful authentication"}
			}
			return nil, &HTTPError{Status: 403, Message: "Server returned 403 after trying upscoping"}
		}
		if err = t.options.Auth.Authorize(ctx, challenge); err != nil {
			return nil, err
		}
	}
}
func (t *httpTransport) decodeMessages(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(b, &batch); err != nil {
			return err
		}
		for _, raw := range batch {
			m, err := decodeMessage(raw)
			if err != nil {
				return err
			}
			t.receive(m)
		}
		return nil
	}
	m, err := decodeMessage(b)
	if err == nil {
		t.receive(m)
	}
	return err
}
func (t *httpTransport) openStream(lastID string, legacy bool, attempt int) {
	if t.ctx.Err() != nil {
		return
	}
	response, err := t.fetchAuthorized(t.ctx, "GET", t.url, nil, lastID)
	if err != nil {
		t.streamError(err, legacy, lastID, attempt)
		return
	}
	if !legacy && response.StatusCode == 405 {
		_ = response.Body.Close()
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		_ = response.Body.Close()
		err = &HTTPError{Status: response.StatusCode, Message: "Failed to open SSE stream: " + http.StatusText(response.StatusCode)}
		t.streamError(err, legacy, lastID, attempt)
		return
	}
	t.consumeStream(response.Body, lastID, true, 0)
}
func origin(u *url.URL) string {
	port := u.Port()
	if port == "80" && u.Scheme == "http" || port == "443" && u.Scheme == "https" {
		port = ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}
func (t *httpTransport) consumeStream(body io.ReadCloser, lastID string, reconnectable bool, attempt int) {
	defer body.Close()
	decoder := protocol.NewSSEDecoder(body)
	priming, receivedResponse := false, false
	for {
		event, err := decoder.Next()
		if err != nil {
			if t.ctx.Err() != nil {
				return
			}
			if err != io.EOF {
				t.report(fmt.Errorf("SSE stream disconnected: %w", err))
			}
			if t.legacy || (reconnectable || priming) && !receivedResponse {
				t.scheduleReconnect(lastID, t.legacy, attempt)
			}
			return
		}
		if event.Retry != nil {
			t.mu.Lock()
			t.retry = time.Duration(*event.Retry) * time.Millisecond
			t.mu.Unlock()
		}
		if event.ID != "" {
			lastID = event.ID
			priming = true
		}
		if t.legacy && event.Event == "endpoint" {
			u, err := url.Parse(event.Data)
			if err == nil {
				u = t.url.ResolveReference(u)
				if origin(u) != origin(t.url) {
					err = fmt.Errorf("Endpoint origin does not match connection origin: %s", u.Scheme+"://"+u.Host)
				}
			}
			if err != nil {
				t.readyOnce.Do(func() { t.endpointReady <- err })
				t.report(err)
				_ = t.close()
				return
			}
			t.mu.Lock()
			t.endpoint = u
			t.mu.Unlock()
			t.readyOnce.Do(func() { t.endpointReady <- nil })
			continue
		}
		if event.Data == "" || event.Event != "" && event.Event != "message" {
			continue
		}
		message, err := decodeMessage([]byte(event.Data))
		if err != nil {
			t.report(err)
			continue
		}
		if message.Method == "" && len(message.Result) > 0 {
			receivedResponse = true
		}
		t.receive(message)
	}
}
func (t *httpTransport) streamError(err error, legacy bool, lastID string, attempt int) {
	if t.ctx.Err() != nil {
		return
	}
	if legacy {
		t.readyOnce.Do(func() { t.endpointReady <- err })
	}
	t.report(err)
	if !legacy && attempt > 0 {
		t.report(fmt.Errorf("Failed to reconnect SSE stream: %w", err))
		t.scheduleReconnect(lastID, legacy, attempt)
	}
}
func (t *httpTransport) scheduleReconnect(lastID string, legacy bool, attempt int) {
	if !legacy && attempt >= t.options.MaxReconnectAttempts {
		t.report(fmt.Errorf("Maximum reconnection attempts (%d) exceeded.", t.options.MaxReconnectAttempts))
		return
	}
	t.mu.Lock()
	delay := t.retry
	t.mu.Unlock()
	if delay == 0 {
		delay = t.options.ReconnectDelay
		for i := 0; i < attempt; i++ {
			delay = time.Duration(float64(delay) * 1.5)
		}
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-t.ctx.Done():
			return
		case <-timer.C:
			t.openStream(lastID, legacy, attempt+1)
		}
	}()
}
func (t *httpTransport) report(err error) {
	if t.onError != nil {
		t.onError(err)
	}
}
func (t *httpTransport) setProtocolVersion(version string) {
	t.mu.Lock()
	t.version = version
	t.mu.Unlock()
}
func (t *httpTransport) sessionID() string { t.mu.Lock(); defer t.mu.Unlock(); return t.session }
func (t *httpTransport) close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		cancel, onClose := t.cancel, t.onClose
		t.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if onClose != nil {
			onClose()
		}
	})
	return nil
}
func (t *httpTransport) terminateSession(ctx context.Context) error {
	if t.sessionID() == "" {
		return nil
	}
	response, err := t.fetchAuthorized(ctx, "DELETE", t.url, nil, "")
	if err != nil {
		t.report(err)
		return err
	}
	defer response.Body.Close()
	if (response.StatusCode < 200 || response.StatusCode >= 300) && response.StatusCode != 405 {
		err = &HTTPError{Status: response.StatusCode, Message: "Failed to terminate session: " + http.StatusText(response.StatusCode)}
		t.report(err)
		return err
	}
	t.mu.Lock()
	t.session = ""
	t.mu.Unlock()
	return nil
}
