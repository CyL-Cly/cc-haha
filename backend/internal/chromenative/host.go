// Package chromenative implements the native-messaging entry point shipped by
// Source. The external MCP server is separately an empty in-process stub.
package chromenative

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const MaxMessageSize = 1024 * 1024
const Version = "1.0.0"

type Options struct {
	Input                           io.ReadCloser
	Output, Error                   io.Writer
	Environment                     map[string]string
	Home, Username, SocketDirectory string
	PID                             int
	Now                             func() time.Time
}

func Username(environment map[string]string) string {
	if account, err := user.Current(); err == nil && account.Username != "" {
		name := account.Username
		if runtime.GOOS == "windows" {
			parts := strings.Split(name, `\`)
			name = parts[len(parts)-1]
		}
		return name
	}
	for _, key := range []string{"USER", "USERNAME"} {
		if environment[key] != "" {
			return environment[key]
		}
	}
	return "default"
}
func SocketDirectory(username string) string { return "/tmp/claude-mcp-browser-bridge-" + username }
func SocketPath(username, directory string, pid int) string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\claude-mcp-browser-bridge-` + username
	}
	if directory == "" {
		directory = SocketDirectory(username)
	}
	return filepath.Join(directory, strconv.Itoa(pid)+".sock")
}

type host struct {
	options  Options
	path     string
	listener net.Listener
	outputMu sync.Mutex
	mu       sync.Mutex
	clients  map[net.Conn]bool
	closed   bool
	workers  sync.WaitGroup
}

func Run(ctx context.Context, o Options) error {
	if o.Input == nil || o.Output == nil {
		return errors.New("Chrome native host requires native stdin and stdout")
	}
	if o.Error == nil {
		o.Error = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	if o.Username == "" {
		o.Username = Username(o.Environment)
	}
	if o.SocketDirectory == "" {
		o.SocketDirectory = SocketDirectory(o.Username)
	}
	h := &host{options: o, path: SocketPath(o.Username, o.SocketDirectory, o.PID), clients: map[net.Conn]bool{}}
	h.log("Initializing...")
	if err := prepareSocketDirectory(o.SocketDirectory); err != nil {
		return err
	}
	listener, err := listenSocket(h.path)
	if err != nil {
		return err
	}
	h.listener = listener
	defer h.close()
	if runtime.GOOS != "windows" {
		_ = os.Chmod(h.path, 0600)
	}
	h.workers.Add(1)
	go h.accept()
	stop := context.AfterFunc(ctx, func() {
		_ = o.Input.Close()
		if output, ok := o.Output.(io.Closer); ok {
			_ = output.Close()
		}
		h.closeConnections()
	})
	defer stop()
	for {
		message, err := readFrame(o.Input)
		if err != nil {
			// Source stops on EOF, stdin errors and invalid zero/oversize
			// framing, leaving malformed JSON to its message validator.
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return nil
		}
		if err := h.chromeMessage(message); err != nil {
			return err
		}
	}
}
func readFrame(reader io.Reader) ([]byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return nil, err
	}
	length := binary.LittleEndian.Uint32(prefix[:])
	if length == 0 || length > MaxMessageSize {
		return nil, errors.New("invalid Chrome native message length")
	}
	message := make([]byte, length)
	_, err := io.ReadFull(reader, message)
	return message, err
}
func writeFrame(writer io.Writer, data []byte) error {
	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], uint32(len(data)))
	packet := append(prefix[:], data...)
	for len(packet) > 0 {
		n, err := writer.Write(packet)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}
func (h *host) send(message protocol.JSONObject) error {
	data, err := message.MarshalJSON()
	if err != nil {
		return err
	}
	h.outputMu.Lock()
	defer h.outputMu.Unlock()
	return writeFrame(h.options.Output, data)
}
func object(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, value any) protocol.JSONField {
	data, _ := json.Marshal(value)
	return protocol.JSONField{Name: name, Value: data}
}
func (h *host) chromeMessage(data []byte) error {
	var message protocol.JSONObject
	var kind string
	if message.UnmarshalJSON(data) != nil || json.Unmarshal(message.Get("type"), &kind) != nil || string(message.Get("type")) == "null" {
		return h.send(object(field("type", "error"), field("error", "Invalid message format")))
	}
	switch kind {
	case "ping":
		return h.send(object(field("type", "pong"), field("timestamp", h.options.Now().UnixMilli())))
	case "get_status":
		return h.send(object(field("type", "status_response"), field("native_host_version", Version)))
	case "tool_response", "notification":
		message.Delete("type")
		body, err := message.MarshalJSON()
		if err != nil {
			return err
		}
		h.mu.Lock()
		clients := make([]net.Conn, 0, len(h.clients))
		for c := range h.clients {
			clients = append(clients, c)
		}
		h.mu.Unlock()
		for _, c := range clients {
			_ = writeFrame(c, body)
		}
		return nil
	default:
		return h.send(object(field("type", "error"), field("error", "Unknown message type: "+kind)))
	}
}
func (h *host) accept() {
	defer h.workers.Done()
	for {
		client, err := h.listener.Accept()
		if err != nil {
			return
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			_ = client.Close()
			return
		}
		h.clients[client] = true
		h.workers.Add(1)
		h.mu.Unlock()
		go h.client(client)
	}
}
func (h *host) client(client net.Conn) {
	defer h.workers.Done()
	defer func() {
		_ = client.Close()
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		_ = h.send(object(field("type", "mcp_disconnected")))
	}()
	if h.send(object(field("type", "mcp_connected"))) != nil {
		return
	}
	for {
		data, err := readFrame(client)
		if err != nil {
			return
		}
		var request protocol.JSONObject
		if !json.Valid(data) || strings.TrimSpace(string(data)) == "null" {
			h.log("Failed to parse tool request")
			continue
		}
		// JavaScript permits property access on arrays and scalar values.
		// Their absent method/params forward a type-only request.
		_ = request.UnmarshalJSON(data)
		// Source forwards method/params without validation, preserving absent
		// properties as JS undefined (omitted from the outgoing JSON).
		message := object(field("type", "tool_request"))
		for _, name := range []string{"method", "params"} {
			if value := request.Get(name); value != nil {
				message.Set(name, value)
			}
		}
		if h.send(message) != nil {
			return
		}
	}
}
func (h *host) closeConnections() {
	h.mu.Lock()
	h.closed = true
	for client := range h.clients {
		_ = client.Close()
	}
	h.mu.Unlock()
	if h.listener != nil {
		_ = h.listener.Close()
	}
}
func (h *host) close() {
	h.closeConnections()
	h.workers.Wait()
	if runtime.GOOS != "windows" {
		_ = os.Remove(h.path)
		_ = os.Remove(h.options.SocketDirectory)
	}
}
func (h *host) log(message string) {
	_, _ = fmt.Fprintln(h.options.Error, "[Claude Chrome Native Host] "+message)
	if h.options.Environment["USER_TYPE"] == "ant" && h.options.Home != "" {
		path := filepath.Join(h.options.Home, ".claude", "debug", "chrome-native-host.txt")
		if file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			_, _ = fmt.Fprintf(file, "[%s] [Claude Chrome Native Host] %s\n", h.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"), message)
			_ = file.Close()
		}
	}
}

func legacyPID(name string) (int, bool) {
	if !strings.HasSuffix(name, ".sock") {
		return 0, false
	}
	value := protocol.TrimJavaScriptString(strings.TrimSuffix(name, ".sock"))
	start := 0
	if strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		start = 1
	}
	end := start
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == start {
		return 0, false
	}
	pid, err := strconv.Atoi(value[:end])
	return pid, err == nil
}
