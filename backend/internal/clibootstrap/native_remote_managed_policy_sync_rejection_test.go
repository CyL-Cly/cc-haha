package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Only the context.AfterFunc delivery to the command's cancellation context is
// held. The actual security dialog, original synchronous failure context, loading
// future, native policy request, terminal key consumer and Close remain real.
// A separate Done channel prevents context's private parent fast path from
// bypassing the public AfterFunc interface; publish closes it synchronously with
// the original cancellation, preserving Err/Value/context.Cause identity.
type managedProcessFailureDeliveryHold struct {
	context.Context
	done       chan struct{}
	registered chan struct{}
	mu         sync.Mutex
	callback   func()
	started    bool
	stopped    bool
	once       sync.Once
	joined     chan struct{}
}

func newManagedProcessFailureDeliveryHold(parent context.Context) *managedProcessFailureDeliveryHold {
	return &managedProcessFailureDeliveryHold{Context: parent, done: make(chan struct{}), registered: make(chan struct{}), joined: make(chan struct{})}
}
func (h *managedProcessFailureDeliveryHold) Done() <-chan struct{} { return h.done }
func (h *managedProcessFailureDeliveryHold) publish() {
	h.once.Do(func() { close(h.done) })
}
func (h *managedProcessFailureDeliveryHold) AfterFunc(callback func()) func() bool {
	h.mu.Lock()
	if h.callback != nil || h.started || h.stopped {
		h.mu.Unlock()
		panic("fixture requires exactly one original process cancellation binding")
	}
	h.callback = callback
	close(h.registered)
	h.mu.Unlock()
	return func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.started || h.stopped {
			return false
		}
		h.stopped = true
		h.callback = nil
		return true
	}
}
func (h *managedProcessFailureDeliveryHold) held() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.started
}
func (h *managedProcessFailureDeliveryHold) releaseAndJoin() {
	h.mu.Lock()
	callback := h.callback
	if h.started {
		h.mu.Unlock()
		<-h.joined
		return
	}
	h.started = true
	h.callback = nil
	h.mu.Unlock()
	if callback != nil {
		go func() { defer close(h.joined); callback() }()
	} else {
		close(h.joined)
	}
	<-h.joined
}

func TestRemoteManagedPolicyMainCommandSynchronousSecurityDecision(t *testing.T) {
	for _, mode := range []string{"reject", "accept", "caller-cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			proxy, ca := claudeAIMCPRefreshPrivateTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"uuid":"owned","checksum":"x","settings":{"apiKeyHelper":"SECRET_NOT_RENDERED"}}`)
			}))
			f := newNativePluginFixture(t)
			delete(f.n.Environment, "ANTHROPIC_BASE_URL")
			f.n.Environment["HTTPS_PROXY"], f.n.Environment["NODE_EXTRA_CA_CERTS"] = proxy, ca
			f.n.Environment["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-owned-env"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			input, writer := io.Pipe()
			output := &managedDialogOutput{ready: make(chan struct{})}
			m, err := NewMainCommand(MainCommandOptions{Arguments: []string{"auth", "status"}, Native: f.n, Platform: "darwin", Version: "2.1.288", PID: os.Getpid(), Input: input, Output: output, Error: io.Discard})
			if err != nil {
				_ = writer.Close()
				_ = input.Close()
				t.Fatal(err)
			}
			s := m.remoteManaged
			hold := newManagedProcessFailureDeliveryHold(s.failureContext)
			originalFail := s.failSecurity
			s.failureContext = hold
			s.failSecurity = func(cause error) { originalFail(cause); hold.publish() }
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			caller, cancelCaller := context.WithCancelCause(ctx)
			done := make(chan error, 1)
			started, joined, closed := false, false, false
			defer func() {
				cancelCaller(context.Canceled)
				cancel()
				_ = writer.Close()
				_ = input.Close()
				hold.releaseAndJoin()
				if !closed {
					m.Close()
				}
				if started && !joined {
					<-done
				}
				if count, _ := s.refs.Snapshot(); count != 0 {
					t.Errorf("physical policy native references escaped Close: %d", count)
				}
			}()
			if err = m.BeginNativeAPIPreconnect(ctx); err != nil {
				t.Fatal(err)
			}
			started = true
			go func() { done <- m.Run(caller, s.wait) }()
			for _, ready := range []<-chan struct{}{hold.registered, output.ready} {
				select {
				case <-ready:
				case <-ctx.Done():
					t.Fatal("actual terminal/cancellation binding not ready", ctx.Err(), output.String())
				}
			}
			if mode == "caller-cancel" {
				cancelCaller(errors.New("original caller cancellation"))
			} else if mode == "close" {
				m.Close()
				closed = true
			} else {
				key := "2"
				if mode == "accept" {
					key = "1"
				}
				if _, err = io.WriteString(writer, key); err != nil {
					t.Fatal(err)
				}
			}
			var result error
			select {
			case result = <-done:
				joined = true
			case <-ctx.Done():
				t.Fatal("public command did not finish at its original boundary", ctx.Err())
			}
			if !hold.held() {
				t.Fatal("fixture released asynchronous cancellation before public result")
			}
			switch mode {
			case "reject":
				if context.Cause(s.failureContext) != ErrManagedPolicyRejected {
					t.Fatal("actual terminal rejection was not recorded synchronously", context.Cause(s.failureContext))
				}
				if result != ErrManagedPolicyRejected {
					t.Fatalf("synchronous managed policy rejection lost public MainCommand result: %v", result)
				}
			case "accept":
				if result != nil || context.Cause(s.failureContext) != nil {
					t.Fatal("actual acceptance changed", result, context.Cause(s.failureContext))
				}
			case "caller-cancel":
				if result != context.Cause(caller) || context.Cause(s.failureContext) != nil {
					t.Fatal("original caller cancellation identity changed", result, context.Cause(caller), context.Cause(s.failureContext))
				}
			case "close":
				if !errors.Is(result, cooperative.ErrClosed) || context.Cause(s.failureContext) != cooperative.ErrClosed {
					t.Fatal("actual close/join lost its original administrative result", result, context.Cause(s.failureContext))
				}
			default:
				t.Fatal(fmt.Errorf("unknown fixture mode %q", mode))
			}
		})
	}
}
