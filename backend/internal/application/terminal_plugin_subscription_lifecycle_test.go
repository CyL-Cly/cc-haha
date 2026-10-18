package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type terminalPluginStartupFailureSession struct {
	*Session
	release  chan struct{}
	callback chan struct{}
	once     sync.Once
	failure  error
}

func (s *terminalPluginStartupFailureSession) SubscribeTerminalPluginInstallation(ctx context.Context, changed func(json.RawMessage)) (json.RawMessage, func(), error) {
	return s.Session.SubscribeTerminalPluginInstallation(ctx, func(value json.RawMessage) { s.once.Do(func() { close(s.callback) }); changed(value) })
}
func (s *terminalPluginStartupFailureSession) BindTerminalAutoConsent(ctx context.Context, emit tui.Emit) (*tui.PermissionRequest, func(), error) {
	close(s.release)
	select {
	case <-s.callback:
		return nil, nil, s.failure
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

func TestTerminalPluginActualNativePublicationBeforeProgramRunStartupErrorUnsubscribesAndJoins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	release, started, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	market := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		defer close(settled)
		select {
		case <-release:
			w.WriteHeader(503)
			_, _ = w.Write([]byte("actual private startup failure"))
		case <-r.Context().Done():
		}
	}))
	defer market.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	prepareTerminalPluginFixture(t, o)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.WriteFile(filepath.Join(config, "settings.json"), raw(map[string]any{"extraKnownMarketplaces": map[string]any{"failure": map[string]any{"source": map[string]any{"source": "url", "url": market.URL}}}}), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual reconciliation never started")
	}
	failure := errors.New("owned later startup binding failure")
	wrapper := &terminalPluginStartupFailureSession{Session: s, release: release, callback: make(chan struct{}), failure: failure}
	finished := make(chan error, 1)
	go func() {
		finished <- tui.Run(ctx, wrapper, tui.Options{Input: strings.NewReader(""), Output: &bytes.Buffer{}, Width: 100, Height: 35})
	}()
	select {
	case err = <-finished:
		if !errors.Is(err, failure) {
			t.Fatal("actual startup error changed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native publication blocked Program.Send before Run; unsubscribe/Close could not join")
	}
	select {
	case <-settled:
	default:
		t.Fatal("actual native HTTP worker was not joined")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if !closed {
		t.Fatal("shared Session remained open after startup failure")
	}
}
