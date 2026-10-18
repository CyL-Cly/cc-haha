//go:build darwin

package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSessionCloseCancelsHeldUserInstructionsBeforeJoiningTurn(t *testing.T) {
	var models atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		models.Add(1)
		http.Error(w, "must not infer after closing held instructions", 500)
	}))
	defer peer.Close()
	options := nativeSessionOptions(t, peer.URL)
	for _, env := range []map[string]string{options.Environment, options.Native.Environment} {
		env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	cleanupCtx, cleanupCancel := context.WithDeadline(context.Background(), deadline)
	defer cleanupCancel()
	session, err := NewSession(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		session.process.BeginNativePromptClose()
		done := make(chan struct{})
		go func() { session.process.Close(); close(done) }()
		select {
		case <-done:
		case <-cleanupCtx.Done():
			t.Error("actual process cleanup exceeded the original total deadline")
		}
	}()
	path := filepath.Join(options.Environment["CLAUDE_CONFIG_DIR"], "CLAUDE.md")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	type openedWriter struct {
		file *os.File
		err  error
	}
	opened := make(chan openedWriter, 1)
	go func() {
		file, err := os.OpenFile(path, os.O_WRONLY, 0600)
		opened <- openedWriter{file: file, err: err}
	}()
	var writer *os.File
	consumed := false
	defer func() {
		cancel()
		if !consumed {
			// Cleanup only: release this fixture's writer after a failed
			// admission without making the real consumer assertion pass.
			rescue, _ := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0600)
			result := <-opened
			writer = result.file
			if rescue != nil {
				_ = rescue.Close()
			}
		}
		if writer != nil {
			_ = writer.Close()
		}
	}()
	turn := make(chan error, 1)
	go func() {
		turn <- session.Submit(ctx, tui.Input{Text: "Use my actual user instructions"}, nil)
	}()
	select {
	case result := <-opened:
		consumed, writer = true, result.file
		if result.err != nil {
			t.Fatal(result.err)
		}
	case err := <-turn:
		t.Fatal("actual turn ended before opening user instructions", err)
	case <-ctx.Done():
		t.Fatal("actual user instruction reader did not open the private FIFO")
	}
	// O_WRONLY completion proves that the actual reader has opened its
	// descriptor. Keep the writer connected and empty throughout Close.
	select {
	case err := <-turn:
		t.Fatal("actual turn finished while its prompt file was held", err)
	default:
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer closeCancel()
	closed := make(chan error, 1)
	go func() { closed <- session.Close(closeCtx) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal("actual Session.Close failed", err)
		}
	case <-closeCtx.Done():
		// Release only after the failure, so cleanup cannot satisfy Close.
		t.Error("Session.Close joined turnMu before retiring the held prompt producer")
		session.process.BeginNativePromptClose()
		_ = writer.Close()
		select {
		case <-closed:
		case <-cleanupCtx.Done():
			t.Error("failed Close did not join within the original total deadline")
		}
		t.FailNow()
	}
	select {
	case err := <-turn:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("actual held prompt turn did not return its cancellation", err)
		}
	case <-ctx.Done():
		t.Fatal("actual closed Session retained its original turn")
	}
	if models.Load() != 0 {
		t.Fatal("closed prompt producer reached the model", models.Load())
	}
	session.mu.Lock()
	closedReceipt := session.closeDone
	session.mu.Unlock()
	select {
	case <-closedReceipt:
	default:
		t.Fatal("Session.Close returned before publishing its actual completion")
	}
}
