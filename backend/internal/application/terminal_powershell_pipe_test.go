package application

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

type powerShellPipeBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *powerShellPipeBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *powerShellPipeBuffer) String() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

type powerShellPipeSession struct {
	*Session
	asks  chan tui.PermissionRequest
	turns chan error
}

func (s *powerShellPipeSession) Submit(ctx context.Context, input tui.Input, emit tui.Emit) error {
	err := s.Session.Submit(ctx, input, func(event tui.Event) error {
		if e := emit(event); e != nil {
			return e
		}
		if event.Kind == tui.PermissionAsked {
			s.asks <- *event.Permission
		}
		return nil
	})
	s.turns <- err
	return err
}

func TestActualInteractiveTUIKeysPowerShellRememberPersistenceAndSecondTurn(t *testing.T) {
	executable := physicalPowerShell(t)
	if _, err := os.Stat(executable); err != nil {
		t.Skip("physical native PowerShell required")
	}
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 || n == 3 {
			response(w, fmt.Sprintf("pipe-write-%d", n), "PowerShell", map[string]any{"command": fmt.Sprintf("touch private-powershell-pipe-%d.txt", n)}, "")
		} else {
			response(w, fmt.Sprintf("pipe-done-%d", n), "", nil, "PRIVATE_PIPE_DONE")
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Interactive = true
	o.Arguments.Tools = []string{"PowerShell"}
	o.Arguments.PermissionMode = "default"
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_CODE_POWERSHELL_PATH"] = executable
		env["CLAUDE_CODE_USE_POWERSHELL_TOOL"] = "1"
		env["TMPDIR"] = o.Native.Home
		env["CLAUDE_CODE_TMPDIR"] = o.Native.Home
	}
	// The catalogue receives a declared Windows discovery seam; every file,
	// shell parser and command below executes physically on this Darwin host.
	o.NativeShells = windowshell.NewNative("windows", o.WorkDir, o.Native.Environment)
	actual, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	host := &powerShellPipeSession{Session: actual, asks: make(chan tui.PermissionRequest, 2), turns: make(chan error, 2)}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output powerShellPipeBuffer
	done := make(chan error, 1)
	go func() {
		done <- tui.Run(ctx, host, tui.Options{Input: reader, Output: &output, Environment: []string{"TERM=dumb", "TERM_PROGRAM=Apple_Terminal"}, InitialPrompt: "Perform the first private write.", Width: 100, Height: 32})
	}()
	select {
	case asked := <-host.asks:
		if asked.ToolName != "PowerShell" || len(asked.Choices) != 3 || !asked.Choices[1].Editable {
			t.Fatal("actual host did not render prefix approval", asked)
		}
	case err := <-done:
		t.Fatal("actual terminal closed before approval", err, output.String())
	case <-ctx.Done():
		t.Fatal(ctx.Err(), output.String())
	}
	// These bytes go through the ordinary terminal reader, key binding/model,
	// edited decision, real Session permission future and settings persistence.
	if _, err := writer.Write([]byte("\x1b[B\x15 touch:* \r")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-host.turns:
		if err != nil {
			t.Fatal(err, output.String())
		}
	case <-ctx.Done():
		t.Fatal("first real terminal turn", ctx.Err(), output.String())
	}
	if _, err := writer.Write([]byte("Perform the second private write.\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-host.turns:
		if err != nil {
			t.Fatal(err, output.String())
		}
	case asked := <-host.asks:
		t.Fatal("remembered actual prefix prompted again", asked, output.String())
	case <-ctx.Done():
		t.Fatal("second real terminal turn", ctx.Err(), output.String())
	}
	writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err, output.String())
		}
	case <-ctx.Done():
		t.Fatal("actual terminal did not drain", ctx.Err())
	}
	if requests.Load() != 4 {
		t.Fatal("actual two-turn request contract", requests.Load())
	}
	file, err := os.ReadFile(filepath.Join(o.WorkDir, "private-powershell-pipe-3.txt"))
	if err != nil || len(file) != 0 {
		t.Fatal("real physical PowerShell consumer", err, string(file))
	}
	settings, err := os.ReadFile(filepath.Join(o.WorkDir, ".claude", "settings.local.json"))
	if err != nil || !strings.Contains(string(settings), "PowerShell(touch:*)") {
		t.Fatal("actual keyboard-edited rule persistence", err, string(settings))
	}
}
