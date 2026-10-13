package computeruse

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Windows helper is a one-shot process. Only its virtual cursor is retained
// until EndTurn; no socket or daemon handshake is part of this protocol.
type windowsInputTagKey struct{}
type windowsClient struct {
	service      *Service
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	closed       bool
	operations   sync.WaitGroup
	bootstrapMu  sync.Mutex
	bootstrapped bool
	tag          string
	badgeMu      sync.Mutex
	badge        *windowsBadge
}

func newWindowsClient(service *Service) *windowsClient {
	ctx, cancel := context.WithCancel(service.ctx)
	var bytes [4]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	tag := binary.LittleEndian.Uint32(bytes[:])
	if tag == 0 {
		tag = 0x43434841
	}
	return &windowsClient{service: service, ctx: ctx, cancel: cancel, tag: strconv.FormatUint(uint64(tag), 10)}
}
func (client *windowsClient) Call(parent context.Context, command string, payload json.RawMessage) (json.RawMessage, error) {
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return nil, context.Canceled
	}
	client.operations.Add(1)
	client.mu.Unlock()
	defer client.operations.Done()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(client.ctx, cancel)
	defer func() { stop(); cancel() }()
	if err := client.bootstrap(ctx); err != nil {
		return nil, err
	}
	payload, err := protocol.ParseAndStringifyJavaScriptJSON(payload)
	if err != nil {
		return nil, err
	}
	if windowsInjectionCommand(command) {
		client.showBadge(ctx, command, payload)
	}
	// execFileNoThrow has the original ten-minute deadline, independent of the
	// macOS daemon's shorter request timeout.
	ctx, deadline := context.WithTimeout(ctx, 10*time.Minute)
	defer deadline()
	ctx = context.WithValue(ctx, windowsInputTagKey{}, client.tag)
	result, runErr := client.service.run(ctx, client.service.windowsPythonPath(), []string{filepath.Join(client.service.windowsRuntimeRoot(), "win_helper.py"), command, "--payload", string(payload)}, nil)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if runErr != nil && result.Code == 0 {
		return nil, runErr
	}
	result.Stdout = windowsOutput(result.Stdout)
	result.Stderr = windowsOutput(result.Stderr)
	return windowsEnvelope(result, command)
}
func windowsOutput(text string) string {
	if !strings.HasSuffix(text, "\n") {
		return text
	}
	text = strings.TrimSuffix(text, "\n")
	return strings.TrimSuffix(text, "\r")
}
func windowsEnvelope(result CommandResult, command string) (json.RawMessage, error) {
	if result.Code != 0 && trim(result.Stdout) == "" {
		message := result.Stderr
		if message == "" {
			message = fmt.Sprintf("Python helper %s failed with code %d", command, result.Code)
		}
		return nil, errors.New(message)
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(result.Stdout))
	if err != nil {
		message := result.Stderr
		if message == "" {
			message = result.Stdout
		}
		if message == "" {
			message = "Python helper " + command + " returned invalid JSON"
		}
		return nil, errors.New(message)
	}
	envelope, ok := decodeObject(normalized)
	if !ok {
		if string(normalized) == "null" {
			return nil, errors.New("null is not an object (evaluating 'parsed.ok')")
		}
		envelope = object{}
	}
	if !truthyRaw(envelope["ok"]) {
		detail, _ := decodeObject(envelope["error"])
		message := pixelQuoted("Python helper " + command + " failed")
		if truthyRaw(detail["message"]) {
			message, err = protocol.JavaScriptStringJSON(detail["message"])
			if err != nil {
				return nil, err
			}
		}
		if code := detail["code"]; truthyRaw(code) && string(code) != `"runtime_error"` {
			message, err = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{code, pixelQuoted(": "), message}, "")
			if err != nil {
				return nil, err
			}
		}
		return nil, &NativeToolError{MessageJSON: message}
	}
	return envelope["result"], nil
}
func (client *windowsClient) bootstrap(ctx context.Context) error {
	client.bootstrapMu.Lock()
	defer client.bootstrapMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if client.bootstrapped {
		return nil
	}
	s := client.service
	root := s.windowsRuntimeRoot()
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	for _, file := range []struct{ from, to string }{{"requirements-win.txt", "requirements.txt"}, {"win_helper.py", "win_helper.py"}, {"win_cursor_badge.py", "win_cursor_badge.py"}} {
		data, err := os.ReadFile(filepath.Join(s.options.ProjectRoot, "runtime", file.from))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(root, file.to), data, 0600); err != nil {
			return err
		}
	}
	run := func(file string, args []string, label string) error {
		commandCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		result, err := s.run(commandCtx, file, args, nil)
		if commandCtx.Err() != nil {
			return commandCtx.Err()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && result.Code == 0 {
			return err
		}
		if result.Code != 0 {
			message := windowsOutput(result.Stderr)
			if message == "" {
				message = windowsOutput(result.Stdout)
			}
			if message == "" {
				message = "unknown error"
			}
			return fmt.Errorf("%s failed with code %d: %s", label, result.Code, message)
		}
		return nil
	}
	python := s.windowsPythonPath()
	if !exists(python) {
		config, err := s.Config(ctx)
		if err != nil {
			return err
		}
		command, _ := stringValue(config["pythonPath"])
		if command == "" {
			command = "python"
		}
		if err = run(command, []string{"-m", "venv", filepath.Join(root, "venv")}, "python venv creation"); err != nil {
			return err
		}
	}
	if !exists(filepath.Join(root, "venv", "Scripts", "pip.exe")) {
		if err := run(python, []string{"-m", "ensurepip", "--upgrade"}, "ensurepip"); err != nil {
			return err
		}
	}
	requirements, err := os.ReadFile(filepath.Join(root, "requirements.txt"))
	if err != nil {
		return err
	}
	digest := requirementsDigest(requirements)
	stamp, _ := os.ReadFile(filepath.Join(root, "requirements.sha256"))
	if trim(string(stamp)) != digest {
		for _, install := range []struct {
			args  []string
			label string
		}{{[]string{"-m", "pip", "install", "--upgrade", "pip"}, "pip upgrade"}, {[]string{"-m", "pip", "install", "-r", filepath.Join(root, "requirements.txt")}, "python dependency install"}} {
			mirror := append(append([]string{}, install.args...), "-i", "https://pypi.tuna.tsinghua.edu.cn/simple/", "--trusted-host", "pypi.tuna.tsinghua.edu.cn")
			first := run(python, mirror, install.label)
			if first != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err = run(python, install.args, install.label); err != nil {
					return first
				}
			}
		}
		if err = os.WriteFile(filepath.Join(root, "requirements.sha256"), []byte(digest+"\n"), 0600); err != nil {
			return err
		}
	}
	client.bootstrapped = true
	return nil
}
func (client *windowsClient) EndTurn(ctx context.Context) error { return client.hideBadge(ctx) }
func (client *windowsClient) Close(ctx context.Context) error {
	client.mu.Lock()
	client.closed = true
	client.cancel()
	client.mu.Unlock()
	badgeErr := client.hideBadge(ctx)
	done := make(chan struct{})
	go func() { client.operations.Wait(); close(done) }()
	select {
	case <-done:
		return badgeErr
	case <-ctx.Done():
		return errors.Join(badgeErr, ctx.Err())
	}
}
func windowsInjectionCommand(command string) bool {
	switch command {
	case "click", "type", "key", "hold_key", "scroll", "drag", "move_mouse", "mouse_down", "mouse_up", "get_app_state", "set_value", "select_text", "perform_secondary_action", "type_text", "press_key":
		return true
	}
	return false
}
