//go:build !windows

package lspruntime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestActualCanceledRequestReleasesPhysicalPeerBeforeNormalClose(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
	if err != nil {
		t.Fatal(err)
	}
	path, receipt, pipe := filepath.Join(home, "private-lsp-peer"), filepath.Join(home, "receipt.jsonl"), filepath.Join(home, "release")
	if err = os.WriteFile(path, script, 0700); err != nil {
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
	peer, err := startClient(path, []string{receipt, home}, home, map[string]string{"HOME": home, "USERPROFILE": home, "PATH": "/usr/bin:/bin", "CC_HAHA_LSP_HOLD_METHOD": "textDocument/hover", "CC_HAHA_LSP_REQUEST_RELEASE": pipe}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.abort()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = peer.request(ctx, "initialize", InitializeParams(os.Getpid(), home, nil)); err != nil {
		t.Fatal(err)
	}
	if err = peer.notification("initialized", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	requestCtx, requestCancel := context.WithCancel(ctx)
	defer requestCancel()
	returned := make(chan error, 1)
	go func() {
		_, failure := peer.request(requestCtx, "textDocument/hover", map[string]any{})
		returned <- failure
	}()
	for {
		data, _ := os.ReadFile(receipt)
		if bytes.Contains(data, []byte(`"method":"textDocument/hover"`)) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real peer never received held request", context.Cause(ctx))
		case <-time.After(time.Millisecond):
		}
	}
	requestCancel()
	select {
	case err = <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("actual admitted request lost its cancellation", err)
		}
	case <-ctx.Done():
		t.Fatal("actual canceled request did not return", context.Cause(ctx))
	}
	closed := make(chan error, 1)
	go func() { closed <- peer.stop(ctx) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		// Release only after observing the product failure, for fixture cleanup.
		_, _ = release.Write([]byte("1"))
		<-closed
		t.Fatal("normal close waited behind an already canceled physical request")
	}
	if peer.command.ProcessState == nil || syscall.Kill(peer.command.Process.Pid, 0) != syscall.ESRCH {
		t.Fatal("canceled request left its actual server process alive")
	}
}
