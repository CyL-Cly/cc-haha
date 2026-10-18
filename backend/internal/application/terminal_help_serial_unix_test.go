//go:build !windows

package application

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalHelpMountedOnDoneCanonicalEnqueueHeldFIFODrainAndNextHuman(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.ReadAll(r.Body)
		response(w, "help-serial", "", nil, "HELP_SERIAL_NEXT")
	}))
	defer peer.Close()
	ctx := context.Background()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	fifoPath := filepath.Join(o.Environment["HOME"], "help-commit-observer.fifo")
	if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
		t.Fatal(err)
	}
	var armed atomic.Bool
	held := make(chan string, 1)
	o.OnCanonicalTranscriptMutation = func(path string) error {
		if !armed.CompareAndSwap(true, false) {
			return nil
		}
		held <- path
		// The existing observer is called only after the actual regular transcript
		// file has been written and closed. Its disposable FIFO receipt holds that
		// observer's completion, without replacing the transcript or its identity.
		file, err := os.OpenFile(fifoPath, os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = file.WriteString(path + "\n")
		return err
	}
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	var reader *os.File
	defer func() {
		if reader == nil {
			reader, _ = os.OpenFile(fifoPath, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		}
		if reader != nil {
			_ = reader.Close()
		}
	}()
	if err = s.Submit(ctx, tui.Input{Text: "actual initial human"}, nil); err != nil {
		t.Fatal(err)
	}
	var path string
	if err = s.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		path, e = s.process.Controller.Writer().ComputedTranscriptPath(scope)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	opened := make(chan tui.Event, 1)
	emit := func(event tui.Event) error {
		if event.Kind == tui.LocalPanelOpened {
			opened <- event
		}
		return nil
	}
	done := make(chan error, 1)
	go func() {
		handled, e := s.InvokeTerminalCommand(ctx, "help", "kept help args", emit)
		if !handled && e == nil {
			e = &tui.UnsupportedAction{Action: "help"}
		}
		done <- e
	}()
	var event tui.Event
	select {
	case event = <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("actual Help did not mount")
	}
	select {
	case e := <-done:
		t.Fatal("Help returned before Source onDone", e)
	case <-time.After(50 * time.Millisecond):
	}
	armed.Store(true)
	if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: event.LocalPanel.ID, Action: "help-close"}, emit); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Source onDone did not release exact mount")
	}
	history, err := s.process.TerminalConversation(ctx)
	if err != nil || !strings.Contains(string(raw(history.Messages)), "kept help args") || !strings.Contains(string(raw(history.Messages)), "Help dialog dismissed") {
		t.Fatal("onDone released before actual canonical history", err, string(raw(history.Messages)))
	}
	select {
	case actual := <-held:
		if actual != path {
			t.Fatal("foreign transcript observer", actual, path)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("actual Writer observer never reached FIFO")
	}
	persisted, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(persisted), "Help dialog dismissed") || !strings.Contains(string(persisted), "kept help args") {
		t.Fatal("actual regular transcript completion absent", err, string(persisted))
	}
	next := make(chan error, 1)
	go func() { next <- s.Submit(ctx, tui.Input{Text: "actual next human"}, nil) }()
	reader, err = os.OpenFile(fifoPath, os.O_RDWR|syscall.O_NONBLOCK, 0600)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 65536)
	receipt := ""
	until := time.Now().Add(3 * time.Second)
	for !strings.Contains(receipt, path) && time.Now().Before(until) {
		n, e := syscall.Read(int(reader.Fd()), buf)
		if n > 0 {
			receipt += string(buf[:n])
		}
		if e != nil && e != syscall.EAGAIN {
			t.Fatal(e)
		}
		if !strings.Contains(receipt, path) {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !strings.Contains(receipt, path) {
		t.Fatal("physical FIFO receipt absent", receipt)
	}
	select {
	case err = <-next:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("same-Harness next human did not settle after observer release")
	}
	if requests.Load() != 2 {
		t.Fatal("Help leaked to model or lost next human", requests.Load())
	}
}
