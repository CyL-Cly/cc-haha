package cliauth_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/keychainprefetchfixture"
)

func TestOrdinaryCLILoginOwnerCloseJoinsPhysicalListenerAndBrowser(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	_ = os.Mkdir(filepath.Join(home, ".claude"), 0700)
	n, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": home, "USER": "fixture", "NODE_ENV": "production", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, errors.New("owned absent") }
	n.StartKeychainPrefetch = keychainprefetchfixture.Bind(n.Temp, n.CWD, n.Home, func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		return n.RunSecurity(ctx, call)
	})
	m, e := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"auth", "login"}, Native: n, Platform: "darwin", Version: "999.0.0-local", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	browserDone := make(chan struct{})
	runDone := make(chan error, 1)
	config, _ := desktopoauth.ResolveConfig(n.Environment)
	go func() {
		runDone <- m.Run(ctx, func(s *cooperative.Scope) error {
			_, e := m.AuthLogin(ctx, s, clibootstrap.AuthLoginOptions{Input: cliauth.LoginInput{}, Config: &config, HTTP: &http.Client{}, Browser: func(ctx context.Context, address string) error {
				u, _ := url.Parse(address)
				ready <- u.Query().Get("redirect_uri")
				<-ctx.Done()
				close(browserDone)
				return context.Cause(ctx)
			}})
			return e
		})
	}()
	var callback string
	select {
	case callback = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("actual owner listener not ready")
	}
	address, _ := url.Parse(callback)
	connection, e := net.DialTimeout("tcp", address.Host, time.Second)
	if e != nil {
		t.Fatal("physical listener before Close", e)
	}
	_ = connection.Close()
	m.Close()
	connection, e = net.DialTimeout("tcp", address.Host, time.Second)
	if e == nil {
		_ = connection.Close()
		t.Error("MainCommand.Close left actual OAuth listener open")
	}
	select {
	case <-browserDone:
	default:
		t.Error("MainCommand.Close did not join its actual browser process boundary")
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary auth owner failed to close")
	}
}
