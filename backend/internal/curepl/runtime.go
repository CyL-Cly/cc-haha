package curepl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Content struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Data      string `json:"data,omitempty"`
	MimeType  string `json:"mimeType,omitempty"`
	textUnits []uint16
}

func (c Content) MarshalJSON() ([]byte, error) {
	row := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quotedContent(c.Type)})
	if c.Type == "text" {
		value := quotedContent(c.Text)
		if c.textUnits != nil {
			value = protocol.QuoteJavaScriptUTF16(c.textUnits)
		}
		row.Set("text", value)
	} else if c.Type == "image" {
		row.Set("data", quotedContent(c.Data))
		row.Set("mimeType", quotedContent(c.MimeType))
	}
	return row.MarshalJSON()
}
func quotedContent(text string) json.RawMessage { raw, _ := json.Marshal(text); return raw }
func (c *Content) UnmarshalJSON(raw []byte) error {
	var row protocol.JSONObject
	if err := row.UnmarshalJSON(raw); err != nil || !row.IsObject() {
		return errors.New("Invalid Computer Use content")
	}
	c.Type = row.String("type")
	if c.Type == "text" {
		units, err := protocol.DecodeJavaScriptJSONString(row.Get("text"))
		if err != nil {
			return err
		}
		c.textUnits = units
		c.Text = row.String("text")
		return nil
	}
	if c.Type == "image" {
		if _, err := protocol.DecodeJavaScriptJSONString(row.Get("data")); err != nil {
			return err
		}
		if _, err := protocol.DecodeJavaScriptJSONString(row.Get("mimeType")); err != nil {
			return err
		}
		c.Data = row.String("data")
		c.MimeType = row.String("mimeType")
		return nil
	}
	return errors.New("Invalid Computer Use content type")
}

type Result struct {
	Content              []Content       `json:"content"`
	IsError              bool            `json:"isError,omitempty"`
	StructuredContent    map[string]any  `json:"structuredContent,omitempty"`
	Meta                 json.RawMessage `json:"_meta,omitempty"`
	StructuredContentRaw json.RawMessage `json:"-"`
}

func (r Result) MarshalJSON() ([]byte, error) {
	content, err := json.Marshal(r.Content)
	if err != nil {
		return nil, err
	}
	row := protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: content})
	if r.IsError {
		row.Set("isError", json.RawMessage("true"))
	}
	structured := r.StructuredContentRaw
	if structured == nil && r.StructuredContent != nil {
		structured, err = json.Marshal(r.StructuredContent)
		if err != nil {
			return nil, err
		}
	}
	if structured != nil {
		row.Set("structuredContent", structured)
	}
	if r.Meta != nil {
		row.Set("_meta", r.Meta)
	}
	return row.MarshalJSON()
}

func (r *Result) UnmarshalJSON(raw []byte) error {
	var row protocol.JSONObject
	if err := row.UnmarshalJSON(raw); err != nil {
		return err
	}
	if err := json.Unmarshal(row.Get("content"), &r.Content); err != nil {
		return err
	}
	r.IsError = string(row.Get("isError")) == "true"
	r.StructuredContentRaw = append(json.RawMessage(nil), row.Get("structuredContent")...)
	if r.StructuredContentRaw != nil {
		if err := json.Unmarshal(r.StructuredContentRaw, &r.StructuredContent); err != nil {
			return err
		}
	}
	r.Meta = append(json.RawMessage(nil), row.Get("_meta")...)
	return nil
}

type Invoke func(context.Context, string, json.RawMessage) (json.RawMessage, error)
type RuntimeOptions struct {
	Executable, TempDir, Platform string
	// Alternate arguments/transport are exclusively for disposable worker
	// process tests. Production always uses the same Go executable and sandbox.
	WorkerArguments []string
	MaxRSSBytes     int64
	HealthCheck     time.Duration
}
type Runtime struct {
	options         RuntimeOptions
	mu              sync.Mutex
	kernel          *kernel
	running, closed bool
	epoch, nextCell int64
	cancel          context.CancelFunc
	idleFailure     string
}
type kernel struct {
	cmd       *exec.Cmd
	directory string
	input     io.WriteCloser
	writeMu   sync.Mutex
	messages  chan workerMessage
	done      chan struct{}
	errMu     sync.Mutex
	err       error
	stderr    string
	stop      sync.Once
	nonce     int64
	heartbeat time.Time
}

func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	if !filepath.IsAbs(options.Executable) || !filepath.IsAbs(options.TempDir) {
		return nil, errors.New("Computer Use JavaScript requires explicit executable and private temporary directory")
	}
	if options.MaxRSSBytes == 0 {
		options.MaxRSSBytes = 768 * 1024 * 1024
	}
	if options.HealthCheck == 0 {
		options.HealthCheck = time.Second
	}
	if options.WorkerArguments == nil {
		options.WorkerArguments = []string{"--computer-use-repl-worker"}
	}
	return &Runtime{options: options}, nil
}
func (k *kernel) send(message workerMessage) error {
	k.writeMu.Lock()
	defer k.writeMu.Unlock()
	select {
	case <-k.done:
		return errors.New("Computer Use JavaScript kernel stopped.")
	default:
	}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = k.input.Write(append(data, '\n'))
	return err
}
func (k *kernel) failure() string {
	k.errMu.Lock()
	defer k.errMu.Unlock()
	if k.err != nil {
		return k.err.Error()
	}
	return "Computer Use JavaScript kernel stopped."
}
func (k *kernel) terminate() {
	k.stop.Do(func() { killProcessGroup(k.cmd); _ = k.input.Close(); <-k.done; _ = os.RemoveAll(k.directory) })
}
func (r *Runtime) start(ctx context.Context, epoch int64) (*kernel, error) {
	r.mu.Lock()
	existing := r.kernel
	r.mu.Unlock()
	if existing != nil {
		return existing, nil
	}
	if r.options.Platform != "darwin" {
		return nil, errors.New("Computer Use JavaScript is currently available on macOS only.")
	}
	directory, err := os.MkdirTemp(r.options.TempDir, "cc-haha-cu-repl-")
	if err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(directory)
		}
	}()
	executable, err := filepath.EvalSymlinks(r.options.Executable)
	if err != nil {
		return nil, err
	}
	profile, err := SandboxProfile(executable, directory)
	if err != nil {
		return nil, err
	}
	args := append([]string{"-p", profile, executable}, r.options.WorkerArguments...)
	cmd := exec.Command("/usr/bin/sandbox-exec", args...)
	cmd.Dir = directory
	cmd.Env = []string{"HOME=" + directory, "TMPDIR=" + directory, "TMP=" + directory, "TEMP=" + directory, "PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "CLAUDE_CONFIG_DIR=" + filepath.Join(directory, ".claude"), "GOTELEMETRY=off"}
	setProcessGroup(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	k := &kernel{cmd: cmd, directory: directory, input: input, messages: make(chan workerMessage, 16), done: make(chan struct{})}
	var reads sync.WaitGroup
	reads.Add(2)
	go func() {
		defer reads.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 1024), 4096)
		for scanner.Scan() {
			k.errMu.Lock()
			k.stderr += scanner.Text() + "\n"
			if len(k.stderr) > 4096 {
				k.stderr = k.stderr[len(k.stderr)-4096:]
			}
			k.errMu.Unlock()
		}
	}()
	go func() {
		defer reads.Done()
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), MaxOutputBytes)
		for scanner.Scan() {
			var message workerMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				k.errMu.Lock()
				k.err = errors.New("Computer Use JavaScript kernel sent invalid protocol data.")
				k.errMu.Unlock()
				killProcessGroup(cmd)
				return
			}
			select {
			case k.messages <- message:
			case <-k.done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			k.errMu.Lock()
			k.err = errors.New("Computer Use JavaScript exceeded the output limit.")
			k.errMu.Unlock()
			killProcessGroup(cmd)
		}
	}()
	go func() {
		err := cmd.Wait()
		k.errMu.Lock()
		if k.err == nil {
			k.err = fmt.Errorf("Computer Use JavaScript kernel exited (%v). %s", err, k.stderr)
		}
		k.errMu.Unlock()
		close(k.done)
	}()
	r.mu.Lock()
	if r.epoch != epoch || r.closed {
		r.mu.Unlock()
		k.terminate()
		return nil, context.Canceled
	}
	r.kernel = k
	r.mu.Unlock()
	cleanup = false
	if err = k.send(workerMessage{Type: "init", Bootstrap: BootstrapSource()}); err != nil {
		k.terminate()
		return nil, err
	}
	select {
	case message := <-k.messages:
		if message.Type != "ready" {
			k.terminate()
			return nil, errors.New("Computer Use JavaScript kernel sent invalid startup data.")
		}
	case <-ctx.Done():
		k.terminate()
		return nil, context.Cause(ctx)
	case <-k.done:
		return nil, errors.New(k.failure())
	}
	go r.health(k)
	return k, nil
}
func (r *Runtime) health(k *kernel) {
	ticker := time.NewTicker(r.options.HealthCheck)
	defer ticker.Stop()
	for {
		select {
		case <-k.done:
			return
		case <-ticker.C:
			r.mu.Lock()
			current := r.kernel == k
			running := r.running
			r.mu.Unlock()
			if !current {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			cmd := exec.CommandContext(ctx, "/bin/ps", "-o", "rss=", "-p", strconv.Itoa(k.cmd.Process.Pid))
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			output, err := cmd.Output()
			cancel()
			if err == nil {
				rss, _ := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
				if rss*1024 > r.options.MaxRSSBytes {
					r.invalidate(k, "Computer Use JavaScript exceeded its memory budget.")
					return
				}
			}
			r.mu.Lock()
			if running && r.kernel == k {
				k.heartbeat = time.Time{}
				r.mu.Unlock()
				continue
			}
			if r.kernel != k || r.running {
				r.mu.Unlock()
				continue
			}
			// Idle response processing is owned here, never a second VM or dispatcher.
			select {
			case message := <-k.messages:
				if message.Type == "pong" && message.Nonce == k.nonce {
					k.heartbeat = time.Time{}
				}
			default:
			}
			if !k.heartbeat.IsZero() {
				if time.Since(k.heartbeat) > 2*time.Second {
					r.mu.Unlock()
					r.invalidate(k, "Computer Use JavaScript left an unresponsive background task.")
					return
				}
			} else {
				k.nonce++
				k.heartbeat = time.Now()
				_ = k.send(workerMessage{Type: "ping", Nonce: k.nonce})
			}
			r.mu.Unlock()
		}
	}
}
func (r *Runtime) invalidate(k *kernel, text string) {
	if text != "" {
		k.errMu.Lock()
		k.err = errors.New(text)
		k.errMu.Unlock()
	}
	r.mu.Lock()
	if r.kernel != k {
		r.mu.Unlock()
		return
	}
	r.kernel = nil
	r.epoch++
	if !r.running {
		r.idleFailure = text + " Bindings were reset. Select the app again."
	}
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	k.terminate()
}
func failureResult(text string) Result {
	return Result{IsError: true, Content: []Content{{Type: "text", Text: text}}}
}
func (r *Runtime) Run(ctx context.Context, code string, timeoutMS int, invoke Invoke, aborted ...func() bool) Result {
	return r.RunUTF16(ctx, utf16.Encode([]rune(code)), timeoutMS, invoke, aborted...)
}

// The MCP input boundary keeps code units from protocol.JSONObject rather than
// converting a lone surrogate into U+FFFD before Acorn or the private compiler.
func (r *Runtime) RunUTF16(ctx context.Context, code []uint16, timeoutMS int, invoke Invoke, aborted ...func() bool) Result {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return failureResult("Computer Use JavaScript kernel stopped.")
	}
	if r.running {
		r.mu.Unlock()
		return failureResult("A Computer Use JavaScript cell is already running.")
	}
	if r.idleFailure != "" {
		text := r.idleFailure
		r.idleFailure = ""
		r.mu.Unlock()
		return failureResult(text)
	}
	if len([]byte(string(utf16.Decode(code)))) > MaxCodeBytes || timeoutMS < 1 || timeoutMS > 60000 || invoke == nil {
		r.mu.Unlock()
		return failureResult("Invalid Computer Use JavaScript code or timeout (maximum 60 seconds).")
	}
	r.running = true
	r.nextCell++
	id, epoch := r.nextCell, r.epoch
	cellCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	r.cancel = cancel
	r.mu.Unlock()
	defer func() { cancel(); r.mu.Lock(); r.running = false; r.cancel = nil; r.mu.Unlock() }()
	content := []Content{}
	bytesOut, actions := 0, 0
	started, completed, rejected := 0, 0, 0
	actionFailed := false
	var statsMu sync.Mutex
	type invocation struct {
		id   int64
		name string
		args json.RawMessage
	}
	tasks := make(chan invocation, MaxActions)
	taskDone := make(chan struct{})
	var k *kernel
	go func() {
		defer close(taskDone)
		for task := range tasks {
			if cellCtx.Err() != nil {
				continue
			}
			statsMu.Lock()
			started++
			statsMu.Unlock()
			result, err := invoke(cellCtx, task.name, task.args)
			var parsed protocol.JSONObject
			if err == nil {
				err = parsed.UnmarshalJSON(result)
				if err == nil && !parsed.IsObject() {
					err = errors.New("Invalid Computer Use native result")
				}
			}
			statsMu.Lock()
			if err != nil {
				actionFailed = true
			} else if string(parsed.Get("isError")) == "true" {
				if string(parsed.Get("nativeCallNotDispatched")) == "true" {
					rejected++
				} else {
					actionFailed = true
				}
			} else {
				completed++
			}
			statsMu.Unlock()
			if cellCtx.Err() != nil {
				continue
			}
			message := workerMessage{Type: "response", CellID: id, RequestID: task.id, Result: result}
			if err != nil {
				message.Result = nil
				message.Error = err.Error()
			}
			_ = k.send(message)
		}
	}()
	finish := func(text string, reset bool) Result {
		if reset {
			cancel()
			if k != nil {
				r.invalidate(k, "")
			}
		}
		close(tasks)
		<-taskDone
		result := Result{Content: content}
		if text != "" {
			statsMu.Lock()
			defer statsMu.Unlock()
			result.IsError = true
			result.Content = append(result.Content, Content{Type: "text", Text: text})
			recovery := "No native actions were dispatched. Resolve the reported error before retrying."
			if started != rejected {
				recovery = "Observe the app before retrying. Completed calls were dispatched, but their visual effect still needs verification."
			}
			result.StructuredContent = map[string]any{"nativeCallsStarted": started, "nativeCallsCompleted": completed, "nativeCallsRejectedBeforeDispatch": rejected, "nativeResultUnknown": actionFailed || started != completed+rejected, "bindingsReset": reset, "recovery": recovery}
			ordered := protocol.NewJSONObject()
			for _, name := range []string{"nativeCallsStarted", "nativeCallsCompleted", "nativeCallsRejectedBeforeDispatch", "nativeResultUnknown", "bindingsReset", "recovery"} {
				value, _ := json.Marshal(result.StructuredContent[name])
				ordered.Set(name, value)
			}
			result.StructuredContentRaw, _ = ordered.MarshalJSON()
		}
		return result
	}
	var err error
	if cellCtx.Err() != nil {
		return finish("Computer Use JavaScript cancelled. Bindings were reset; observe the app before continuing.", true)
	}
	for _, check := range aborted {
		if check != nil && check() {
			return finish("Computer Use JavaScript cancelled. Bindings were reset; observe the app before continuing.", true)
		}
	}
	k, err = r.start(cellCtx, epoch)
	if err != nil {
		if cellCtx.Err() != nil {
			return finish("Computer Use JavaScript cancelled. Bindings were reset; observe the app before continuing.", true)
		}
		return finish("Computer Use JavaScript startup failed: "+err.Error(), true)
	}
	raw := protocol.QuoteJavaScriptUTF16(code)
	if err = k.send(workerMessage{Type: "run", CellID: id, Code: raw}); err != nil {
		return finish(err.Error(), true)
	}
	requests := map[int64]bool{}
	validTools := map[string]bool{"list_apps": true, "get_app_state": true, "click": true, "drag": true, "scroll": true, "press_key": true, "type_text": true, "paste": true, "set_value": true, "select_text": true, "perform_secondary_action": true}
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-poll.C:
			for _, check := range aborted {
				if check != nil && check() {
					return finish("Computer Use JavaScript cancelled. Bindings were reset; observe the app before continuing.", true)
				}
			}
		case <-cellCtx.Done():
			text := "Computer Use JavaScript cancelled. Bindings were reset; observe the app before continuing."
			if errors.Is(cellCtx.Err(), context.DeadlineExceeded) {
				text = "Computer Use JavaScript timed out. Bindings were reset; observe the app before continuing."
			}
			if k != nil {
				failure := k.failure()
				if failure == "Computer Use JavaScript exceeded its memory budget." || failure == "Computer Use JavaScript reset. Bindings were discarded." {
					text = failure
				}
			}
			return finish(text, true)
		case <-k.done:
			return finish(k.failure(), true)
		case message := <-k.messages:
			if message.Type == "pong" {
				r.mu.Lock()
				if message.Nonce == k.nonce {
					k.heartbeat = time.Time{}
				}
				r.mu.Unlock()
				continue
			}
			if message.CellID != id {
				continue
			}
			switch message.Type {
			case "done":
				return finish(message.Error, false)
			case "emit":
				var block Content
				if json.Unmarshal(message.Content, &block) != nil || block.Type != "text" && block.Type != "image" || block.Type == "image" && block.MimeType != "image/png" && block.MimeType != "image/jpeg" && block.MimeType != "image/webp" {
					return finish("Computer Use JavaScript emitted invalid content.", true)
				}
				bytesOut += len(bytes.TrimSpace(message.Content))
				if bytesOut > MaxOutputBytes || len(content) >= 128 {
					return finish("Computer Use JavaScript exceeded the output limit.", true)
				}
				content = append(content, block)
			case "invoke":
				actions++
				if message.RequestID < 0 || message.RequestID > 9007199254740991 || requests[message.RequestID] || !validTools[message.Name] || actions > MaxActions {
					return finish("Computer Use JavaScript exceeded its action limit or requested an invalid operation.", true)
				}
				requests[message.RequestID] = true
				tasks <- invocation{message.RequestID, message.Name, message.Args}
			default:
				return finish("Computer Use JavaScript kernel sent invalid protocol data.", true)
			}
		}
	}
}
func (r *Runtime) Reset() {
	r.mu.Lock()
	r.epoch++
	k := r.kernel
	r.kernel = nil
	r.idleFailure = ""
	cancel := r.cancel
	r.mu.Unlock()
	if k != nil {
		k.errMu.Lock()
		k.err = errors.New("Computer Use JavaScript reset. Bindings were discarded.")
		k.errMu.Unlock()
	}
	if cancel != nil {
		cancel()
	}
	if k != nil {
		k.terminate()
	}
}
func (r *Runtime) Close() { r.mu.Lock(); r.closed = true; r.mu.Unlock(); r.Reset() }
