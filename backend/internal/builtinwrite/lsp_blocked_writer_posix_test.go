//go:build !windows

package builtinwrite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualWriteCommitsBeforeBlockedLSPPipeAndCloseReapsThatWriter(t *testing.T) {
	s, owner, _, _, home := fixture(t)
	peerPath, receipt, pipe := filepath.Join(home, "private-lsp-peer"), filepath.Join(home, "lsp-blocked-frames.jsonl"), filepath.Join(home, "change-release")
	script, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(peerPath, script, 0700); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	release, err := os.OpenFile(pipe, os.O_RDWR|syscall.O_NONBLOCK, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	manager, err := lspruntime.New(lspruntime.Options{Owner: owner, Binding: s.options.NativeBinding, CWD: home, PID: os.Getpid(), Environment: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "CC_HAHA_LSP_HOLD_METHOD": "textDocument/didChange", "CC_HAHA_LSP_REQUEST_RELEASE": pipe}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	manager.Configure([]lspruntime.ServerConfig{{Name: "private", Config: protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quoted(peerPath)}, protocol.JSONField{Name: "args", Value: quotedList([]string{receipt, home})}, protocol.JSONField{Name: "extensionToLanguage", Value: json.RawMessage(`{".txt":"plaintext"}`)})}}, nil)
	path := filepath.Join(home, "blocked-notification.txt")
	if err = os.WriteFile(path, []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = manager.Open(path, quoted("original\n")); err != nil {
		t.Fatal(err)
	}
	options := s.options
	options.LSP = manager
	options.HostState = func(*cooperative.Scope) (Host, error) { return Host{NonInteractive: true, LSPEnabled: true}, nil }
	actual, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	write := func(content string) {
		t.Helper()
		run(t, owner, func(scope *cooperative.Scope) error {
			if err := actual.ReadForCache(scope, quoted(path)); err != nil {
				return err
			}
			if _, err := actual.Call(context.Background(), input(path, content), scope); err != nil {
				return err
			}
			cache, found, err := options.Cache.Get(scope, path)
			if err != nil || !found || string(cache.Content) != string(quoted(content)) {
				return errors.New("actual Write did not settle shared Read cache independently")
			}
			return nil
		})
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Fatal("actual Write did not commit before language-server notification", err)
		}
	}
	write("hold the private peer\n")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pid := 0
	for pid == 0 {
		for _, frame := range heldWriteLSPFrames(t, receipt) {
			if frame.String("method") == "textDocument/didChange" {
				if err := json.Unmarshal(frame.Get("pid"), &pid); err != nil {
					t.Fatal(err)
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual server never entered its held notification", context.Cause(ctx))
		case <-time.After(time.Millisecond):
		}
	}
	// The peer is blocked before its next stdin read. This real notification is
	// larger than the physical pipe buffer and cannot settle until peer cleanup.
	write(strings.Repeat("actual private LSP backpressure\n", 70000))
	manager.BeginClose()
	closed := make(chan error, 1)
	go func() { closed <- manager.Close(ctx) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		// Release only after the original window fails, solely to drain fixtures.
		_, _ = release.Write([]byte("1"))
		<-closed
		t.Fatal("close waited behind an actual blocked notification pipe writer")
	}
	if err := manager.JoinNotifications(ctx); err != nil || !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		t.Fatal("actual notification job or server outlived close", err, pid)
	}
}
