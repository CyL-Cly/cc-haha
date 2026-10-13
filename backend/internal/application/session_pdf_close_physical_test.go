package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSessionCloseCancelsRuntimePDFBeforeJoiningPendingASK(t *testing.T) {
	var calls atomic.Int32
	var path string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) != 1 {
			t.Error("runtime close advanced pending PDF to a new request")
			http.Error(w, "must not infer", 500)
			return
		}
		response(w, "pdf-close-held", "Read", map[string]any{"file_path": path}, "")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	bin := filepath.Join(o.WorkDir, "owned-poppler")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(o.WorkDir, "pdf-process-started")
	script := "#!/bin/sh\nprintf 'STARTED' > '" + marker + "'; /bin/sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "pdfinfo"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	o.Environment["PATH"] = bin + ":/usr/bin:/bin"
	o.Native.Environment["PATH"] = o.Environment["PATH"]
	path = filepath.Join(o.WorkDir, "admitted.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\nORIGINAL_SOURCE_HEADER_ADMITTED\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	turn := make(chan error, 1)
	go func() { turn <- s.Submit(context.Background(), tui.Input{Text: "Read the actual owned PDF"}, nil) }()
	deadline := time.After(5 * time.Second)
	timer := time.NewTicker(5 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			if _, err := os.Stat(marker); err == nil {
				goto started
			}
		case err := <-turn:
			t.Fatal("actual PDF failed before physical process admission", err)
		case <-deadline:
			t.Fatal("actual PDF native process did not start")
		}
	}
started:
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(closeCtx); err != nil {
		t.Fatal("Close waited for PDF timeout instead of its physical process join", err)
	}
	select {
	case <-turn:
	case <-closeCtx.Done():
		t.Fatal("Close left original ASK continuation alive")
	}
	if calls.Load() != 1 {
		t.Fatal("PDF close replayed or inferred", calls.Load())
	}
	s.mu.Lock()
	closed, receipt := s.closed, s.closeDone
	s.mu.Unlock()
	if !closed || receipt == nil {
		t.Fatal("actual Session Close did not finish")
	}
	select {
	case <-receipt:
	default:
		t.Fatal("actual Close receipt was not settled")
	}
}
