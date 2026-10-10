package computeruse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ProtocolVersion = "CCHahaComputerUseIPC-2"

var regexpKeySpaces = regexp.MustCompile(`\s*\+\s*`)

type DaemonUnavailableError struct{ Message string }

func (err *DaemonUnavailableError) Error() string { return err.Message }

type ResultUnknownError struct {
	Command string
	Cause   error
}

func (err *ResultUnknownError) Error() string {
	return fmt.Sprintf("cu-helper daemon command %s lost its response; execution result is unknown: %v", err.Command, err.Cause)
}
func (err *ResultUnknownError) Unwrap() error { return err.Cause }

type daemonClient struct {
	service        *Service
	sessionID      string
	operation      sync.Mutex
	mu             sync.Mutex
	connection     *net.UnixConn
	reader         *bufio.Reader
	socketPath     string
	pid            int
	nextID         uint64
	turnID         string
	overlayKey     string
	closed         bool
	grantRestarted bool
}

func newDaemonClient(service *Service, sessionID string) *daemonClient {
	return &daemonClient{service: service, sessionID: sessionID}
}
func privateRuntimeDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("cu-helper runtime directory is a symbolic link: " + path)
	}
	if !info.IsDir() {
		return errors.New("cu-helper runtime path is not a directory: " + path)
	}
	if !ownedByCurrentUser(info) {
		return errors.New("cu-helper runtime directory is not owned by the current user: " + path)
	}
	if err = os.Chmod(path, 0700); err != nil {
		return err
	}
	info, err = os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0700 {
		return errors.New("cu-helper runtime directory is not private: " + path)
	}
	return nil
}
func (client *daemonClient) start(ctx context.Context) error {
	client.mu.Lock()
	closed, existing := client.closed, client.connection
	client.mu.Unlock()
	if closed {
		return &DaemonUnavailableError{"cu-helper daemon client is closed"}
	}
	if existing != nil {
		return nil
	}
	binary, err := client.service.ResolveLaunchableHelper(ctx)
	if err != nil {
		return &DaemonUnavailableError{err.Error()}
	}
	bundle := helperBundle(binary)
	if binary == "" || bundle == "" {
		return &DaemonUnavailableError{"cu-helper .app bundle not found"}
	}
	runtimeRoot := filepath.Join(client.service.options.ConfigDir, ".runtime")
	if err = privateRuntimeDirectory(runtimeRoot); err != nil {
		return &DaemonUnavailableError{err.Error()}
	}
	client.service.mu.Lock()
	client.service.nextGeneration++
	generation := client.service.nextGeneration
	client.service.mu.Unlock()
	socket := filepath.Join(runtimeRoot, fmt.Sprintf("cu-helper.daemon.%d.%d.sock", os.Getpid(), generation))
	// sockaddr_un is limited to 104 bytes on macOS. Keep the original short
	// generation counter and process owner so an old daemon cannot unlink new.
	if len(socket) > 100 {
		return &DaemonUnavailableError{"cu-helper runtime socket path exceeds the native macOS limit"}
	}
	client.service.reapStaleDaemons(ctx, socket)
	readyCtx, cancel := context.WithTimeout(ctx, client.service.options.ReadinessTimeout)
	defer cancel()
	result, err := client.service.run(readyCtx, "/usr/bin/open", []string{"-n", bundle, "--args", "daemon", "--socket", socket}, nil)
	if err != nil || result.Code != 0 {
		return &DaemonUnavailableError{fmt.Sprintf("cu-helper LaunchServices start failed: %s (%v)", trim(result.Stderr), err)}
	}
	var connection *net.UnixConn
	for connection == nil {
		conn, connectErr := (&net.Dialer{}).DialContext(readyCtx, "unix", socket)
		if connectErr == nil {
			connection, _ = conn.(*net.UnixConn)
			if connection == nil {
				_ = conn.Close()
				return &DaemonUnavailableError{"cu-helper did not use an AF_UNIX connection"}
			}
			break
		}
		select {
		case <-readyCtx.Done():
			return &DaemonUnavailableError{"cu-helper daemon socket not ready: " + readyCtx.Err().Error()}
		case <-time.After(100 * time.Millisecond):
		}
	}
	peerPID, err := client.attest(readyCtx, connection, binary)
	if err != nil {
		_ = connection.Close()
		return &DaemonUnavailableError{err.Error()}
	}
	raw, err := os.ReadFile(socket + ".pid")
	pid, parseErr := strconv.Atoi(trim(string(raw)))
	if err != nil || parseErr != nil || pid <= 0 || pid != peerPID {
		_ = connection.Close()
		return &DaemonUnavailableError{"cu-helper daemon pidfile does not match the authenticated socket peer"}
	}
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		_ = connection.Close()
		return &DaemonUnavailableError{"cu-helper daemon client closed during startup"}
	}
	client.connection, client.reader, client.socketPath, client.pid = connection, bufio.NewReader(connection), socket, pid
	client.mu.Unlock()
	response, err := client.dispatch(ctx, "ping", json.RawMessage(`{}`))
	var hello struct {
		ProtocolVersion string `json:"protocolVersion"`
		Absolute        bool   `json:"supportsAbsoluteDeadlines"`
		TurnEnd         bool   `json:"supportsTurnEnd"`
	}
	if err != nil || json.Unmarshal(response, &hello) != nil || hello.ProtocolVersion != ProtocolVersion || !hello.Absolute || !hello.TurnEnd {
		client.retire()
		return &DaemonUnavailableError{fmt.Sprintf("cu-helper protocol negotiation failed: %s (%v)", response, err)}
	}
	return nil
}

var staleDaemonName = regexp.MustCompile(`^cu-helper\.daemon\.([1-9][0-9]*)(?:\.[1-9][0-9]*)?\.sock\.pid$`)

func (service *Service) reapStaleDaemons(ctx context.Context, current string) {
	entries, err := os.ReadDir(filepath.Dir(current))
	if err != nil {
		return
	}
	for _, entry := range entries {
		matches := staleDaemonName.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		owner, _ := strconv.Atoi(matches[1])
		pidfile := filepath.Join(filepath.Dir(current), entry.Name())
		socket := strings.TrimSuffix(pidfile, ".pid")
		if socket == current || processAlive(owner) {
			continue
		}
		raw, err := os.ReadFile(pidfile)
		pid, parseErr := strconv.Atoi(trim(string(raw)))
		if err != nil || parseErr != nil || pid <= 0 || !processAlive(pid) {
			_ = os.Remove(socket)
			_ = os.Remove(pidfile)
			continue
		}
		result, err := service.run(ctx, "/bin/ps", []string{"-o", "pid=,comm=", "-p", strconv.Itoa(pid)}, nil)
		if err != nil || result.Code != 0 {
			continue
		}
		verified := false
		for _, line := range strings.Split(result.Stdout, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 && fields[0] == strconv.Itoa(pid) && strings.Contains(strings.Join(fields[1:], " "), "cc-haha-computer-use") {
				verified = true
				break
			}
		}
		if verified {
			_, _ = service.run(ctx, "/bin/kill", []string{"-TERM", strconv.Itoa(pid)}, nil)
			_ = os.Remove(socket)
			_ = os.Remove(pidfile)
		}
	}
}

func (client *daemonClient) attest(ctx context.Context, connection *net.UnixConn, expectedBinary string) (int, error) {
	file, err := connection.File()
	if err != nil {
		return 0, err
	}
	defer file.Close()
	canonical, err := filepath.EvalSymlinks(expectedBinary)
	if err != nil {
		return 0, err
	}
	verifier := client.service.ResolveSourceHelper()
	if verifier == "" {
		return 0, errors.New("cu-helper peer verifier binary is missing")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := client.service.run(ctx, verifier, []string{"attest_daemon_peer", "--payload", string(encode(map[string]string{"executablePath": canonical}))}, []*os.File{file})
	// os/exec calls File.Fd while preparing ExtraFiles, which switches the
	// shared socket description to blocking. Restore O_NONBLOCK AFTER that
	// verifier launch; restoring before Run is overwritten by ExtraFiles.
	if restoreErr := restoreNonblocking(connection); restoreErr != nil {
		return 0, restoreErr
	}
	if err != nil || result.Code != 0 || len(result.Stdout) > 64*1024 || len(result.Stderr) > 64*1024 {
		return 0, errors.New("cu-helper daemon socket peer failed reverse code-signature attestation")
	}
	raw, err := parseEnvelope("attest_daemon_peer", result)
	if err != nil {
		return 0, err
	}
	var identity struct {
		Trusted bool `json:"trusted"`
		PID     int  `json:"pid"`
	}
	if json.Unmarshal(raw, &identity) != nil || !identity.Trusted || identity.PID <= 0 {
		return 0, errors.New("cu-helper daemon socket peer failed reverse code-signature attestation")
	}
	return identity.PID, nil
}
func isConnectionCommand(command string) bool {
	return command == "ping" || command == "check_permissions" || command == "list_installed_apps" || command == "shutdown"
}
func (client *daemonClient) dispatch(ctx context.Context, command string, payload json.RawMessage) (json.RawMessage, error) {
	client.mu.Lock()
	connection, reader := client.connection, client.reader
	if connection == nil {
		client.mu.Unlock()
		return nil, &DaemonUnavailableError{"cu-helper daemon socket closed before command dispatch"}
	}
	client.nextID++
	id := strconv.FormatUint(client.nextID, 10)
	if !isConnectionCommand(command) && client.turnID == "" {
		client.turnID = randomID()
	}
	turnID := client.turnID
	if turnID == "" {
		turnID = "connection-" + filepath.Base(client.socketPath)
	}
	client.mu.Unlock()
	timeout := client.service.options.RequestTimeout
	if command == "press_key" {
		body, _ := decodeObject(payload)
		key, _ := stringValue(body["key"])
		if len(strings.Fields(regexpKeySpaces.ReplaceAllString(trim(key), "+"))) > 1 {
			timeout *= 3
			if timeout > 60*time.Second {
				timeout = 60 * time.Second
			}
		}
	}
	deadline := time.Now().Add(timeout)
	if current, ok := ctx.Deadline(); ok && current.Before(deadline) {
		deadline = current
	}
	request := encode(map[string]any{"id": id, "requestId": id, "cmd": command, "payload": json.RawMessage(payload), "clientApiVersion": ProtocolVersion, "deadlineUnixMilliseconds": deadline.UnixMilli(), "sessionId": client.sessionID, "turnId": turnID})
	_ = connection.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = connection.SetDeadline(time.Now()) })
	defer stop()
	n, err := connection.Write(append(request, '\n'))
	if err != nil || n != len(request)+1 {
		client.retire()
		if n == 0 {
			return nil, &DaemonUnavailableError{fmt.Sprintf("cu-helper socket write failed: %v", err)}
		}
		return nil, &ResultUnknownError{command, err}
	}
	for {
		line, err := readDaemonLine(reader)
		if err != nil {
			client.retire()
			return nil, &ResultUnknownError{command, err}
		}
		var envelope struct {
			ID     string                         `json:"id"`
			OK     bool                           `json:"ok"`
			Result json.RawMessage                `json:"result"`
			Error  struct{ Message, Code string } `json:"error"`
		}
		if json.Unmarshal(line, &envelope) != nil || envelope.ID != id {
			continue
		}
		if command == "turn_end" || command == "overlay_hide" {
			client.mu.Lock()
			if client.turnID == turnID {
				client.turnID = ""
			}
			client.mu.Unlock()
		}
		if !envelope.OK {
			if envelope.Error.Message == "" {
				envelope.Error.Message = "cu-helper daemon command failed"
			}
			return nil, &NativeCommandError{envelope.Error.Message, envelope.Error.Code}
		}
		if len(envelope.Result) == 0 {
			envelope.Result = encode(nil)
		}
		return envelope.Result, nil
	}
}
func readDaemonLine(reader *bufio.Reader) ([]byte, error) {
	line := []byte{}
	for {
		part, err := reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > 128*1024*1024 {
			return nil, errors.New("cu-helper daemon frame exceeded 128 MiB")
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

var injectionCommands = map[string]bool{"click": true, "type": true, "key": true, "hold_key": true, "scroll": true, "drag": true, "move_mouse": true, "mouse_down": true, "mouse_up": true, "get_app_state": true, "set_value": true, "select_text": true, "perform_secondary_action": true, "type_text": true, "press_key": true}

func overlayTarget(payload json.RawMessage) json.RawMessage {
	value, _ := decodeObject(payload)
	var pid float64
	if json.Unmarshal(value["pid"], &pid) == nil && pid > 0 && pid < 9007199254740992 && pid == float64(int64(pid)) {
		return encode(object{"pid": value["pid"]})
	}
	for _, key := range []string{"bundleId", "app"} {
		if text, ok := stringValue(value[key]); ok && trim(text) != "" {
			return encode(object{key: value[key]})
		}
	}
	return json.RawMessage(`{}`)
}
func (client *daemonClient) Call(ctx context.Context, command string, payload json.RawMessage) (json.RawMessage, error) {
	client.operation.Lock()
	defer client.operation.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := client.start(ctx); err != nil {
		return nil, err
	}
	if injectionCommands[command] {
		target := overlayTarget(payload)
		client.mu.Lock()
		key := client.overlayKey
		client.mu.Unlock()
		if key != string(target) {
			if _, err := client.dispatch(ctx, "overlay_show", target); err == nil {
				client.mu.Lock()
				client.overlayKey = string(target)
				client.mu.Unlock()
			}
		}
	}
	result, err := client.dispatch(ctx, command, payload)
	if err == nil {
		client.grantRestarted = false
	} else if !client.grantRestarted && (strings.Contains(strings.ToLower(err.Error()), "not_trusted") || strings.Contains(strings.ToLower(err.Error()), "accessibility permission")) {
		client.grantRestarted = true
		client.retire()
	}
	return result, err
}
func (client *daemonClient) EndTurn(ctx context.Context) error {
	client.operation.Lock()
	defer client.operation.Unlock()
	client.mu.Lock()
	existing := client.connection != nil
	active := client.turnID != ""
	client.mu.Unlock()
	if !existing || !active {
		return nil
	}
	_, err := client.dispatch(ctx, "turn_end", json.RawMessage(`{}`))
	client.mu.Lock()
	client.overlayKey = ""
	client.mu.Unlock()
	if err != nil {
		client.retire()
	}
	return err
}
func (client *daemonClient) retire() {
	client.mu.Lock()
	connection, socket, pid := client.connection, client.socketPath, client.pid
	client.connection, client.reader, client.socketPath, client.pid = nil, nil, "", 0
	client.turnID = ""
	client.overlayKey = ""
	client.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
	if pid > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		probe, err := client.service.options.Run(ctx, "/bin/ps", []string{"-o", "pid=,comm=", "-p", strconv.Itoa(pid)}, nil)
		if err == nil && probe.Code == 0 {
			for _, line := range strings.Split(probe.Stdout, "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == strconv.Itoa(pid) && strings.Contains(strings.Join(fields[1:], " "), "cc-haha-computer-use") {
					_, _ = client.service.options.Run(ctx, "/bin/kill", []string{"-TERM", strconv.Itoa(pid)}, nil)
					break
				}
			}
		}
	}
	if socket != "" {
		_ = os.Remove(socket + ".pid")
		_ = os.Remove(socket)
	}
}
func (client *daemonClient) Close(ctx context.Context) error {
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
	// Retire immediately to interrupt a dispatched call when shutdown cannot
	// acquire its serial input boundary. Its response is then result-unknown.
	locked := make(chan struct{})
	go func() { client.operation.Lock(); close(locked) }()
	select {
	case <-locked:
		defer client.operation.Unlock()
		client.mu.Lock()
		existing := client.connection != nil
		client.mu.Unlock()
		if existing {
			deadline, cancel := context.WithTimeout(ctx, time.Second)
			_, _ = client.dispatch(deadline, "shutdown", json.RawMessage(`{}`))
			cancel()
		}
		client.retire()
		return nil
	case <-ctx.Done():
		client.retire()
		go func() { <-locked; client.operation.Unlock() }()
		return ctx.Err()
	}
}
