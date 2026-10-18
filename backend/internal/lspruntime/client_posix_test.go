//go:build !windows

package lspruntime

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

type observedLSPInput struct {
	io.WriteCloser
	mu   sync.Mutex
	data []byte
}

func (w *observedLSPInput) Write(data []byte) (int, error) {
	n, err := w.WriteCloser.Write(data)
	w.mu.Lock()
	w.data = append(w.data, data[:n]...)
	w.mu.Unlock()
	return n, err
}
func TestActualStdioNormalStopWritesExitAndReapsItsPeer(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../testdata/lsp-file-notify-peer.py")
	if err != nil {
		t.Fatal(err)
	}
	path, receipt := filepath.Join(home, "private-lsp-peer"), filepath.Join(home, "receipt.jsonl")
	if err = os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	peer, err := startClient(path, []string{receipt, home}, home, map[string]string{"HOME": home, "USERPROFILE": home, "PATH": "/usr/bin:/bin"}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.abort()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = peer.request(ctx, "initialize", InitializeParams(os.Getpid(), home, nil)); err != nil {
		t.Fatal(err)
	}
	if err = peer.notification("initialized", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	peer.writeJobs.Wait()
	observed := &observedLSPInput{WriteCloser: peer.input}
	peer.input = observed
	if err = peer.stop(ctx); err != nil {
		t.Fatal(err)
	}
	observed.mu.Lock()
	data := append([]byte{}, observed.data...)
	observed.mu.Unlock()
	if !bytes.Contains(data, []byte(`"method":"shutdown"`)) || !bytes.Contains(data, []byte(`"method":"exit"`)) {
		t.Fatal("actual JSON-RPC output did not write shutdown then exit", string(data))
	}
	if bytes.Index(data, []byte(`"method":"shutdown"`)) > bytes.Index(data, []byte(`"method":"exit"`)) {
		t.Fatal("exit preceded actual shutdown")
	}
	if peer.command.ProcessState == nil || syscall.Kill(peer.command.Process.Pid, 0) != syscall.ESRCH {
		t.Fatal("actual native peer not reaped")
	}
}
