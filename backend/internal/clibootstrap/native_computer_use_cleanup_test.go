package clibootstrap

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// Only the OS engine and peer attestation are fixtures. The real native CU
// Service, socket framing/turn IDs, Binder, Host cleanup and job ledger run.
func TestNativeMainPrintGlobalCloseCancelsHeldActualComputerUseEndTurnAndJoins(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(fmt.Sprintf("close-before-cleanup=%v", before), func(t *testing.T) { testNativeMainPrintComputerUseClose(t, before) })
	}
}

func testNativeMainPrintComputerUseClose(t *testing.T, before bool) {
	f, _, options := computerUseMainFixture(t)
	root, err := os.MkdirTemp("/tmp", "ccu-cleanup-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	config := filepath.Join(root, "config")
	helper := filepath.Join(root, "cc-haha-computer-use.app", "Contents", "MacOS", "cc-haha-computer-use")
	for _, dir := range []string{config, filepath.Dir(helper), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(helper, []byte("private OS primitive; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	held, drained := make(chan struct{}), make(chan struct{})
	var turnEnds, nativeLaunches atomic.Int32
	var listener net.Listener
	service, err := computeruse.New(computeruse.Options{HomeDir: root, ConfigDir: config, WorkDir: f.cwd, TempDir: filepath.Join(root, "tmp"), Platform: "darwin", Arch: "arm64", Environment: map[string]string{"HOME": root, "CLAUDE_CONFIG_DIR": config, "PATH": "/usr/bin:/bin", "CC_HAHA_CU_HELPER_PATH": helper}, Run: func(ctx context.Context, file string, args []string, extra []*os.File) (computeruse.CommandResult, error) {
		switch file {
		case "/usr/bin/uname":
			return computeruse.CommandResult{Stdout: "25.4.0"}, nil
		case "/bin/ps":
			// This fixture has no native child process. Never identify the Go
			// test's own PID as an externally launched helper to terminate.
			return computeruse.CommandResult{Code: 1}, nil
		case "/usr/bin/open":
			if len(args) != 6 || args[0] != "-n" || args[2] != "--args" || args[3] != "daemon" || args[4] != "--socket" {
				return computeruse.CommandResult{}, fmt.Errorf("unexpected launch %v", args)
			}
			nativeLaunches.Add(1)
			var err error
			listener, err = net.Listen("unix", args[5])
			if err != nil {
				return computeruse.CommandResult{}, err
			}
			if err := os.WriteFile(args[5]+".pid", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				return computeruse.CommandResult{}, err
			}
			go func() {
				defer close(drained)
				connection, err := listener.Accept()
				if err != nil {
					t.Error(err)
					return
				}
				defer connection.Close()
				scanner := bufio.NewScanner(connection)
				for scanner.Scan() {
					var request protocol.JSONObject
					if err := request.UnmarshalJSON(scanner.Bytes()); err != nil {
						t.Error(err)
						return
					}
					command := request.String("cmd")
					if command == "turn_end" {
						if request.String("sessionId") != "11111111-1111-4111-8111-111111111111" || request.String("turnId") == "" {
							t.Error("cleanup lost actual native turn identity", string(scanner.Bytes()))
						}
						turnEnds.Add(1)
						close(held)
						var next [1]byte
						_, err := connection.Read(next[:])
						if err != io.EOF {
							t.Error("global close did not close held native peer", err)
						}
						return
					}
					result := map[string]any{"axText": "private native state"}
					if command == "ping" {
						result = map[string]any{"protocolVersion": computeruse.ProtocolVersion, "supportsAbsoluteDeadlines": true, "supportsTurnEnd": true}
					}
					data, _ := json.Marshal(map[string]any{"id": request.String("id"), "ok": true, "result": result})
					if _, err := connection.Write(append(data, '\n')); err != nil {
						t.Error(err)
						return
					}
				}
			}()
			return computeruse.CommandResult{}, nil
		default:
			if file == helper && len(args) > 0 && args[0] == "attest_daemon_peer" && len(extra) == 1 {
				info, err := extra[0].Stat()
				if err != nil || info.Mode()&os.ModeSocket == 0 {
					return computeruse.CommandResult{}, fmt.Errorf("native attestation did not carry actual socket: %v", err)
				}
				return computeruse.CommandResult{Stdout: fmt.Sprintf(`{"ok":true,"result":{"trusted":true,"pid":%d}}`, os.Getpid())}, nil
			}
			return computeruse.CommandResult{}, fmt.Errorf("unexpected external primitive %s %v", file, args)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = service.Close(context.Background())
		if listener != nil {
			_ = listener.Close()
		}
	})
	if err := service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	options.Service = service
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"cleanup\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	m, err := NewMainPrint(MainPrintOptions{ComputerUse: options, Arguments: PrintArguments{Model: "fixture-model", SystemPrompt: "private held cleanup", Tools: []string{"Read"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 1, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func([]byte) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownSession(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Call(ctx, m.Controller.SessionID(), "get_app_state", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if before {
		m.BeginNativeComputerUseCleanupClose()
		if err := m.ShutdownSession(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case <-drained:
		case <-ctx.Done():
			t.Fatal("close-before-cleanup retained native peer", ctx.Err())
		}
		// Cancellation may race an already-started socket write. Either
		// zero or one final turn_end is valid; no new daemon or retained
		// native request may survive the physical EOF/Host.Join above.
		if nativeLaunches.Load() != 1 || turnEnds.Load() > 1 {
			t.Fatal("closed host duplicated cleanup", nativeLaunches.Load(), turnEnds.Load())
		}
		return
	}
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, mainObj(json.RawMessage(`{"type":"user","message":{"role":"user","content":"finish actual model turn"}}`)))
	}()
	select {
	case <-held:
	case err := <-done:
		t.Fatal("actual turn_end did not remain held", err)
	case <-ctx.Done():
		t.Fatal("actual turn_end never arrived", ctx.Err())
	}
	// This is Session.Close's pre-turnMu cancellation boundary, followed by
	// its existing current-turn join and same MainPrint shutdown consumer.
	m.BeginNativeComputerUseCleanupClose()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("global close waited on the independent native cleanup", ctx.Err())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("Host.Join left actual native peer alive", ctx.Err())
	}
	if nativeLaunches.Load() != 1 || turnEnds.Load() != 1 {
		t.Fatal("cleanup launched extra daemon or duplicated turn_end", nativeLaunches.Load(), turnEnds.Load())
	}
}
