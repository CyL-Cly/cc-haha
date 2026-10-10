package application

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSessionSandboxSyntheticFutureHostAnswerIgnoresToolUpdatesAndCloseJoins(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Arguments.PermissionMode = "default"
	o.Native.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
	o.Native.Environment["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Native.Home, "private-tmp")
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, fmt.Errorf("isolated fixture Keychain unavailable")
	}
	config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"sandbox":{"enabled":true,"failIfUnavailable":true,"network":{"allowedDomains":[]}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	var permissionFuture interface {
		Wait(context.Context) (bool, error)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, e := s.process.Host.StartNativeSandboxNetwork(ctx, scope, "held.owned.test")
		permissionFuture = f
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	pending := s.PendingPermissions()
	if len(pending) != 1 || pending[0].ToolName != "SandboxNetworkAccess" {
		t.Fatal("actual session did not use same pending map", pending)
	}
	// Socket abort is deliberately unrelated to the process-lifetime context.
	client, cancelClient := context.WithCancel(context.Background())
	cancelClient()
	if client.Err() == nil || len(s.PendingPermissions()) != 1 {
		t.Fatal("disconnect removed Source's pending synthetic request")
	}
	before := ""
	e = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p, e := s.process.Host.NativePermissionContext(scope)
		before = p.String("mode")
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RespondPermission(context.Background(), pending[0].ID, tui.PermissionDecision{Allowed: true, UpdatedInput: raw(map[string]any{"different": "ignored"}), PermissionUpdates: raw([]any{map[string]any{"type": "setMode", "mode": "bypassPermissions", "destination": "session"}})}); e != nil {
		t.Fatal(e)
	}
	if allowed, e := permissionFuture.Wait(context.Background()); e != nil || !allowed {
		t.Fatal(allowed, e)
	}
	if len(s.PendingPermissions()) != 0 {
		t.Fatal("settled synthetic remained pending")
	}
	e = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p, e := s.process.Host.NativePermissionContext(scope)
		if e == nil && p.String("mode") != before {
			e = fmt.Errorf("synthetic request applied ordinary tool updates")
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	// A second lifetime is administrative, and Close must settle it before the
	// owner is released. It cannot wait on an abandoned human/SDK response.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	e = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, e := s.process.Host.StartNativeSandboxNetwork(ctx2, scope, "close.owned.test")
		permissionFuture = f
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	cancel2()
	deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if allowed, e := permissionFuture.Wait(deadline); e != nil || allowed {
		t.Fatal("administrative cancellation", allowed, e)
	}
	if e = s.Close(deadline); e != nil {
		t.Fatal(e)
	}
}

// This deliberately creates a real proxy socket while retaining Source's
// disconnected host request. No model, external destination, or account is used.
func TestActualSessionSandboxProxyDisconnectedClientAndAdministrativeClose(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Arguments.PermissionMode = "default"
	o.Native.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
	o.Native.Environment["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Native.Home, "private-tmp")
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, fmt.Errorf("isolated fixture Keychain unavailable")
	}
	config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
	os.MkdirAll(config, 0700)
	os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"sandbox":{"enabled":true,"failIfUnavailable":true,"network":{"allowedDomains":[]}}}`), 0600)
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	// Capture the actual manager port through the same HTTP Hook producer;
	// the accessor below exposes only immutable listening ports.
	port, _, e := s.process.NativeSandboxProxyPorts(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	c, e := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if e != nil {
		t.Fatal(e)
	}
	fmt.Fprint(c, "GET http://disconnected.owned.test/original HTTP/1.1\r\nHost: disconnected.owned.test\r\n\r\n")
	deadline := time.After(5 * time.Second)
	for len(s.PendingPermissions()) != 1 {
		select {
		case <-deadline:
			c.Close()
			t.Fatal("real proxy did not deliver actual pending SDK permission")
		case <-time.After(time.Millisecond):
		}
	}
	c.Close()
	if len(s.PendingPermissions()) != 1 {
		t.Fatal("disconnected proxy erased host obligation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = s.Close(ctx); e != nil {
		t.Fatal("real proxy pending future prevented actual Close join", e)
	}
	if len(s.PendingPermissions()) != 0 {
		t.Fatal("actual owner shutdown left pending permission")
	}
	conn, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if e == nil {
		conn.Close()
		t.Fatal("closed native proxy remained listening")
	}
}
