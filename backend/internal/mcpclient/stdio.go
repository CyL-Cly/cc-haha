package mcpclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type stdioTransport struct {
	mu        sync.Mutex
	writeMu   sync.Mutex
	config    Config
	options   Options
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	done      chan struct{}
	closeOnce sync.Once
	stderr    strings.Builder
	onError   func(error)
	closed    bool
}

func newStdioTransport(config Config, options Options) *stdioTransport {
	return &stdioTransport{config: config, options: options, done: make(chan struct{})}
}
func (t *stdioTransport) start(ctx context.Context, receive func(rpcMessage), onError func(error), onClose func()) error {
	if t.config.Command == "" {
		return errors.New("Command cannot be empty")
	}
	command, args := t.config.Command, append([]string(nil), t.config.Args...)
	if prefix := t.options.Environment["CLAUDE_CODE_SHELL_PREFIX"]; prefix != "" {
		command = prefix
		args = []string{strings.Join(append([]string{t.config.Command}, args...), " ")}
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = t.options.CWD
	configureMCPProcess(cmd)
	env := map[string]string{}
	for k, v := range t.options.Environment {
		env[k] = v
	}
	for k, v := range t.config.Env {
		env[k] = v
	}
	if err := prepareMCPCommand(cmd, env, t.options.CWD); err != nil {
		return err
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cmd.Env = []string{}
	for _, k := range keys {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return err
	}
	t.mu.Lock()
	if t.closed || ctx.Err() != nil {
		t.mu.Unlock()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return context.Canceled
	}
	if err = cmd.Start(); err != nil {
		t.mu.Unlock()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return err
	}
	t.cmd = cmd
	t.stdin = stdin
	t.onError = onError
	t.mu.Unlock()
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 && err == nil {
				message, e := decodeMessage(line)
				if e != nil {
					onError(e)
				} else {
					receive(message)
				}
			}
			if err != nil {
				if err != io.EOF {
					onError(err)
				}
				return
			}
		}
	}()
	go func() {
		defer readers.Done()
		buffer := make([]byte, 32*1024)
		for {
			n, err := stderr.Read(buffer)
			if n > 0 {
				t.mu.Lock()
				if t.stderr.Len() < 64*1024*1024 {
					remaining := 64*1024*1024 - t.stderr.Len()
					if n > remaining {
						n = remaining
					}
					_, _ = t.stderr.Write(buffer[:n])
				}
				t.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { readers.Wait(); _ = cmd.Wait(); close(t.done); onClose() }()
	go func() {
		select {
		case <-ctx.Done():
			_ = t.close()
		case <-t.done:
		}
	}()
	return nil
}

func lookupCommand(name, path, cwd string) (string, error) {
	for _, directory := range filepath.SplitList(path) {
		if directory == "" {
			directory = "."
		}
		if !filepath.IsAbs(directory) && cwd != "" {
			directory = filepath.Join(cwd, directory)
		}
		candidate := filepath.Join(directory, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("spawn %s ENOENT", name)
}
func (t *stdioTransport) send(ctx context.Context, m rpcMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b := raw(m)
	b = append(b, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.mu.Lock()
	stdin := t.stdin
	t.mu.Unlock()
	if stdin == nil {
		return errors.New("Not connected")
	}
	_, err := stdin.Write(b)
	if err != nil && t.onError != nil {
		t.onError(err)
	}
	return err
}
func (t *stdioTransport) close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		cmd, stdin := t.cmd, t.stdin
		t.stdin = nil
		t.mu.Unlock()
		if cmd == nil || cmd.Process == nil {
			return
		}
		// Source cleanup signals explicit stdio before closing the SDK pipe:
		// SIGINT, 100ms grace, SIGTERM, 400ms grace, then SIGKILL.
		_ = signalMCPProcess(cmd, 0)
		wait := func(d time.Duration) bool {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-t.done:
				return true
			case <-timer.C:
				return false
			}
		}
		if !wait(100 * time.Millisecond) {
			_ = signalMCPProcess(cmd, 1)
			if !wait(400 * time.Millisecond) {
				_ = signalMCPProcess(cmd, 2)
			}
		}
		if stdin != nil {
			_ = stdin.Close()
		}
		_ = wait(2 * time.Second)
	})
	return nil
}
func (t *stdioTransport) setProtocolVersion(string) {}
func (t *stdioTransport) pid() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd == nil || t.cmd.Process == nil {
		return 0
	}
	select {
	case <-t.done:
		return 0
	default:
		return t.cmd.Process.Pid
	}
}
func (t *stdioTransport) stderrText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stderr.String()
}
