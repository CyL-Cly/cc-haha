package cliauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
)

type authorizationCode struct {
	Code string
	Err  error
}
type callbackReply struct {
	Status int
	Header http.Header
	Body   string
}
type pendingCallback struct {
	reply chan callbackReply
	done  chan struct{}
}

// OAuthListener is the original temporary localhost redirect capture server.
// A valid callback remains physically pending until the client exchange finishes.
type OAuthListener struct {
	mu            sync.Mutex
	path, state   string
	errorRedirect string
	listener      net.Listener
	server        *http.Server
	port          int
	codes         chan authorizationCode
	pending       *pendingCallback
	stopped       chan struct{}
	close         sync.Once
	readyDone     chan struct{}
	serveDone     chan struct{}
}

func NewOAuthListener(path string) *OAuthListener {
	return &OAuthListener{path: path, errorRedirect: "https://platform.claude.com/oauth/code/success?app=claude-code", codes: make(chan authorizationCode, 1), stopped: make(chan struct{})}
}
func (l *OAuthListener) Start(port int) (int, error) {
	listener, e := net.Listen("tcp", net.JoinHostPort("localhost", strconv.Itoa(port)))
	if e != nil {
		return 0, fmt.Errorf("Failed to start OAuth callback server: %s", e)
	}
	l.listener = listener
	l.port = listener.Addr().(*net.TCPAddr).Port
	l.server = &http.Server{Handler: http.HandlerFunc(l.redirect)}
	l.serveDone = make(chan struct{})
	go func() {
		defer close(l.serveDone)
		if e := l.server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) && !errors.Is(e, net.ErrClosed) {
			l.deliver(authorizationCode{Err: e})
		}
	}()
	return l.port, nil
}
func (l *OAuthListener) Wait(ctx context.Context, state string, onReady func() error) (string, error) {
	l.mu.Lock()
	l.state = state
	l.readyDone = make(chan struct{})
	l.mu.Unlock()
	ready := make(chan error, 1)
	go func() { defer close(l.readyDone); ready <- onReady() }()
	select {
	case e := <-ready:
		if e != nil {
			return "", e
		}
		select {
		case result := <-l.codes:
			return result.Code, result.Err
		case <-ctx.Done():
			return "", context.Cause(ctx)
		case <-l.stopped:
			return "", context.Canceled
		}
	case result := <-l.codes:
		return result.Code, result.Err
	case <-ctx.Done():
		return "", context.Cause(ctx)
	case <-l.stopped:
		return "", context.Canceled
	}
}
func (l *OAuthListener) deliver(result authorizationCode) {
	select {
	case l.codes <- result:
	default:
	}
}
func (l *OAuthListener) redirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != l.path {
		w.WriteHeader(404)
		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	if code == "" {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("Authorization code not found"))
		l.deliver(authorizationCode{Err: errors.New("No authorization code received")})
		return
	}
	l.mu.Lock()
	expected := l.state
	l.mu.Unlock()
	if state != expected {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("Invalid state parameter"))
		l.deliver(authorizationCode{Err: errors.New("Invalid state parameter")})
		return
	}
	pending := &pendingCallback{reply: make(chan callbackReply, 1), done: make(chan struct{})}
	defer close(pending.done)
	l.mu.Lock()
	l.pending = pending
	l.mu.Unlock()
	l.deliver(authorizationCode{Code: code})
	select {
	case reply := <-pending.reply:
		for key, values := range reply.Header {
			w.Header()[key] = append([]string{}, values...)
		}
		w.WriteHeader(reply.Status)
		_, _ = w.Write([]byte(reply.Body))
	case <-r.Context().Done():
	case <-l.stopped:
	}
}
func (l *OAuthListener) SetErrorRedirect(address string) {
	l.mu.Lock()
	l.errorRedirect = address
	l.mu.Unlock()
}
func (l *OAuthListener) HasPending() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.pending != nil }
func (l *OAuthListener) Respond(status int, headers http.Header, body string) {
	l.mu.Lock()
	pending := l.pending
	l.pending = nil
	l.mu.Unlock()
	if pending != nil {
		pending.reply <- callbackReply{status, headers, body}
		select {
		case <-pending.done:
		case <-l.stopped:
		}
	}
}
func (l *OAuthListener) Close() {
	l.close.Do(func() {
		l.mu.Lock()
		redirect := l.errorRedirect
		l.mu.Unlock()
		l.Respond(302, http.Header{"Location": []string{redirect}}, "")
		close(l.stopped)
		if l.listener != nil {
			_ = l.listener.Close()
		}
		if l.server != nil {
			_ = l.server.Shutdown(context.Background())
		}
		if l.serveDone != nil {
			<-l.serveDone
		}
		l.mu.Lock()
		ready := l.readyDone
		l.mu.Unlock()
		if ready != nil {
			<-ready
		}
	})
}
