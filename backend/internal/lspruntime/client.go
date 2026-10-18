// Package lspruntime owns the native plugin language-server processes and their
// original LSP document/diagnostic consumers. Loading declarations never starts
// a process; the first document or request does.
package lspruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type response struct {
	result json.RawMessage
	err    error
}
type queuedWrite struct {
	data []byte
	done chan error
}
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

type client struct {
	mu                sync.Mutex
	writeMu           sync.Mutex
	writes            []*queuedWrite
	writing           bool
	writeClosed       bool
	writeJobs         sync.WaitGroup
	command           *exec.Cmd
	input             io.WriteCloser
	done              chan struct{}
	readDone          chan struct{}
	stderrDone        chan struct{}
	requests          map[int64]chan response
	next              int64
	exit              error
	stopping          bool
	initialized       bool
	notify            func(string, json.RawMessage)
	onCrash           func(error)
	notificationError func(string, error)
}

func executable(command, cwd string, env map[string]string) string {
	if strings.ContainsAny(command, `/\`) {
		return command
	}
	for _, directory := range filepath.SplitList(env["PATH"]) {
		if directory == "" {
			directory = cwd
		}
		path := filepath.Join(directory, command)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path
		}
	}
	return command
}

func startClient(command string, args []string, cwd string, environment map[string]string, notify func(string, json.RawMessage), crash func(error), notificationError func(string, error), debug func(string)) (*client, error) {
	cmd := exec.Command(executable(command, cwd, environment), args...)
	cmd.Dir = cwd
	cmd.Env = make([]string, 0, len(environment))
	for k, v := range environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	// Start confirms the OS spawn before any LSP frame is written, matching the
	// Source spawn/error event boundary (including executable-not-found).
	if err = cmd.Start(); err != nil {
		_ = input.Close()
		return nil, err
	}
	c := &client{command: cmd, input: input, done: make(chan struct{}), readDone: make(chan struct{}), stderrDone: make(chan struct{}), requests: map[int64]chan response{}, notify: notify, onCrash: crash, notificationError: notificationError}
	go func() {
		defer close(c.stderrDone)
		buffer := make([]byte, 32768)
		for {
			n, err := stderr.Read(buffer)
			if n > 0 && debug != nil {
				value := strings.TrimSpace(strings.ToValidUTF8(string(buffer[:n]), "\ufffd"))
				if value != "" {
					debug(value)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go c.read(output)
	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		c.exit = err
		c.initialized = false
		stopping := c.stopping
		c.mu.Unlock()
		c.fail(errors.New("LSP server connection closed"))
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() > 0 && !stopping && c.onCrash != nil {
			c.onCrash(fmt.Errorf("LSP server crashed with exit code %d", cmd.ProcessState.ExitCode()))
		}
		close(c.done)
	}()
	return c, nil
}

func (c *client) send(value any) error {
	return <-c.enqueue(value)
}
func (c *client) enqueue(value any) <-chan error {
	done := make(chan error, 1)
	data, err := json.Marshal(value)
	if err != nil {
		done <- err
		return done
	}
	c.writeMu.Lock()
	if c.writeClosed {
		c.writeMu.Unlock()
		done <- errors.New("LSP server connection closed")
		return done
	}
	c.writes = append(c.writes, &queuedWrite{data, done})
	if !c.writing {
		c.writing = true
		c.writeJobs.Add(1)
		go c.flushWrites()
	}
	c.writeMu.Unlock()
	return done
}
func (c *client) flushWrites() {
	defer c.writeJobs.Done()
	for {
		c.writeMu.Lock()
		if len(c.writes) == 0 {
			c.writing = false
			c.writeMu.Unlock()
			return
		}
		item := c.writes[0]
		c.writeMu.Unlock()
		_, err := fmt.Fprintf(c.input, "Content-Length: %d\r\n\r\n", len(item.data))
		if err == nil {
			_, err = c.input.Write(item.data)
		}
		c.writeMu.Lock()
		c.writes = c.writes[1:]
		c.writeMu.Unlock()
		item.done <- err
	}
}
func (c *client) notification(method string, params any) error {
	return c.enqueueNotification(method, params)()
}
func (c *client) enqueueNotification(method string, params any) func() error {
	done := c.enqueue(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	return func() error {
		if err := <-done; err != nil && c.notificationError != nil {
			c.notificationError(method, err)
		}
		return nil
	}
}
func (c *client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	id := c.next
	c.next++
	result := make(chan response, 1)
	c.requests[id] = result
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.requests, id); c.mu.Unlock() }()
	write := c.enqueue(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case err := <-write:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		c.abort()
		return nil, context.Cause(ctx)
	}
	select {
	case out := <-result:
		return out.result, out.err
	case <-ctx.Done():
		c.abort()
		return nil, context.Cause(ctx)
	case <-c.done:
		return nil, errors.New("LSP server connection closed")
	}
}
func (c *client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.requests {
		select {
		case ch <- response{err: err}:
		default:
		}
	}
}
func (c *client) read(output io.Reader) {
	defer close(c.readDone)
	defer func() { c.mu.Lock(); c.initialized = false; c.mu.Unlock() }()
	r := bufio.NewReader(output)
	for {
		length := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				c.fail(err)
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			key, value, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(key, "Content-Length") {
				length, err = strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					c.fail(err)
					return
				}
			}
		}
		if length < 0 {
			c.fail(errors.New("LSP frame missing Content-Length"))
			return
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(r, data); err != nil {
			c.fail(err)
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			continue
		}
		if message.Method != "" {
			if message.ID != nil {
				var result any
				if message.Method == "workspace/configuration" {
					var p struct {
						Items []json.RawMessage `json:"items"`
					}
					_ = json.Unmarshal(message.Params, &p)
					result = make([]any, len(p.Items))
				} else {
					_ = c.enqueue(map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": &RPCError{Code: -32601, Message: "Unhandled method " + message.Method}})
					continue
				}
				_ = c.enqueue(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
			} else if c.notify != nil {
				c.notify(message.Method, message.Params)
			}
			continue
		}
		var id int64
		if json.Unmarshal(message.ID, &id) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.requests[id]
		c.mu.Unlock()
		if ch != nil {
			out := response{result: message.Result}
			if message.Error != nil {
				out.err = message.Error
			}
			select {
			case ch <- out:
			default:
			}
		}
	}
}
func (c *client) stop(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	_, err := c.request(ctx, "shutdown", map[string]any{})
	if err == nil {
		err = c.notification("exit", map[string]any{})
	}
	_ = c.input.Close()
	c.writeMu.Lock()
	c.writeClosed = true
	c.writeMu.Unlock()
	// Source disposes the JSON-RPC connection then terminates its real process,
	// even when shutdown fails. Join all physical readers before releasing it.
	_ = terminateProcess(c.command.Process)
	<-c.done
	<-c.readDone
	<-c.stderrDone
	c.writeJobs.Wait()
	return err
}
func (c *client) abort() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	c.writeMu.Lock()
	c.writeClosed = true
	c.writeMu.Unlock()
	_ = c.input.Close()
	_ = c.command.Process.Kill()
}
