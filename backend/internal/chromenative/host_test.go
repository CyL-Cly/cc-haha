package chromenative

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type synchronizedOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (o *synchronizedOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.Buffer.Write(data)
}
func (o *synchronizedOutput) snapshot() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.Buffer.Bytes()...)
}
func (o *synchronizedOutput) waitMessage(t *testing.T, index int) protocol.JSONObject {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		reader := bytes.NewReader(o.snapshot())
		for i := 0; i <= index; i++ {
			data, err := readFrame(reader)
			if err != nil {
				break
			}
			if i == index {
				var value protocol.JSONObject
				if value.UnmarshalJSON(data) != nil {
					t.Fatal(string(data))
				}
				return value
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("framed native output did not arrive", index, string(o.snapshot()))
	return protocol.JSONObject{}
}
func startHost(t *testing.T) (*io.PipeWriter, *synchronizedOutput, string, context.CancelFunc, <-chan error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket integration; Windows pipe has platform-specific tests")
	}
	// Darwin sockaddr_un paths are shorter than the hermetic HOME/TMPDIR.
	// Allocate a fresh, explicitly owned short directory and remove it below.
	root, err := os.MkdirTemp("/tmp", "cc-ch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	directory := filepath.Join(root, "bridge")
	input, writer := io.Pipe()
	output := &synchronizedOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Input: input, Output: output, SocketDirectory: directory, Username: "owned", PID: os.Getpid(), Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		close(done)
	}()
	path := SocketPath("owned", directory, os.Getpid())
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatal("host failed startup", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("native socket not listening")
		}
		time.Sleep(time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		_ = writer.Close()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("native host did not join on shutdown")
		}
	})
	return writer, output, path, cancel, done
}

func TestChromeNativeHostFramedValidationStatusAndSourceFields(t *testing.T) {
	input, output, _, _, _ := startHost(t)
	for i, row := range []struct{ request, kind, field, value string }{
		{`{"type":"ping"}`, "pong", "timestamp", "1700000000000"},
		{`{"type":"get_status"}`, "status_response", "native_host_version", `"1.0.0"`},
		{`{broken`, "error", "error", `"Invalid message format"`},
		{`{"type":3}`, "error", "error", `"Invalid message format"`},
		{`{"type":"unsupported"}`, "error", "error", `"Unknown message type: unsupported"`},
	} {
		if err := writeFrame(input, []byte(row.request)); err != nil {
			t.Fatal(err)
		}
		message := output.waitMessage(t, i)
		if message.String("type") != row.kind || string(message.Get(row.field)) != row.value {
			t.Fatal(row, message)
		}
	}
}

func TestChromeNativeHostActualSocketFanoutPermissionsAndCleanup(t *testing.T) {
	input, output, path, _, done := startHost(t)
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("socket directory permissions", info, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket permissions", info, err)
	}
	first, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if output.waitMessage(t, 0).String("type") != "mcp_connected" {
		t.Fatal("first connection missing")
	}
	second, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if output.waitMessage(t, 1).String("type") != "mcp_connected" {
		t.Fatal("second connection missing")
	}
	request := []byte(`{"method":"owned_tool","params":{"tab":17},"discard":"Source omits this"}`)
	var packet bytes.Buffer
	if err := writeFrame(&packet, request); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range [][]byte{packet.Bytes()[:2], packet.Bytes()[2:7], packet.Bytes()[7:]} {
		if _, err := first.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	actual := output.waitMessage(t, 2)
	if actual.String("type") != "tool_request" || actual.String("method") != "owned_tool" || string(actual.Get("params")) != `{"tab":17}` || actual.Get("discard") != nil {
		t.Fatal(actual)
	}
	for _, row := range []string{`{"type":"tool_response","id":"owned-id","result":{"ready":true}}`, `{"type":"notification","method":"owned_notification","params":[17]}`} {
		if err := writeFrame(input, []byte(row)); err != nil {
			t.Fatal(err)
		}
		for _, client := range []net.Conn{first, second} {
			_ = client.SetReadDeadline(time.Now().Add(time.Second))
			data, err := readFrame(client)
			if err != nil {
				t.Fatal(err)
			}
			var response protocol.JSONObject
			_ = response.UnmarshalJSON(data)
			if response.Get("type") != nil || response.Get("id") == nil && response.String("method") != "owned_notification" {
				t.Fatal("broadcast did not strip type", response)
			}
		}
	}
	_ = first.Close()
	if output.waitMessage(t, 3).String("type") != "mcp_disconnected" {
		t.Fatal("disconnect missing")
	}
	_ = input.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("EOF did not stop socket and join clients")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("socket leaked", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("empty socket directory leaked", err)
	}
}

func TestChromeNativeHostInvalidFramesAndCancelStopActualInput(t *testing.T) {
	for _, length := range []uint32{0, MaxMessageSize + 1} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			input, _, path, _, done := startHost(t)
			var prefix [4]byte
			binary.LittleEndian.PutUint32(prefix[:], length)
			_, _ = input.Write(prefix[:])
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("invalid framing did not terminate")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("framing error leaked host")
			}
		})
	}
	input, _, _, cancel, done := startHost(t)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancel blocked stdin")
	}
	if _, err := input.Write([]byte("closed")); err == nil {
		t.Fatal("cancel did not close actual reader")
	}
}

func TestChromeNativeHostLegacyDirectoryMigrationAndStaleSocketSweep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix legacy socket paths")
	}
	directory := filepath.Join(t.TempDir(), "bridge")
	if err := os.WriteFile(directory, []byte("legacy socket file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketDirectory(directory); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"2147483647.sock", "2147483647suffix.sock", "not-pid.sock", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	live := filepath.Join(directory, fmt.Sprint(os.Getpid())+".sock")
	if err := os.WriteFile(live, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketDirectory(directory); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"2147483647.sock", "2147483647suffix.sock"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatal("Source parseInt stale cleanup", name, err)
		}
	}
	for _, name := range []string{"not-pid.sock", "keep.txt", filepath.Base(live)} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatal("live/non-socket entry deleted", name, err)
		}
	}
}
