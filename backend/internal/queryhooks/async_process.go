package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeAsyncProcess struct {
	mu           sync.Mutex
	command      *exec.Cmd
	output       *asyncTaskOutput
	done         chan struct{}
	changed      chan struct{}
	supervised   chan struct{}
	stopProgress chan struct{}
	cleanupOnce  sync.Once
	transferred  chan struct{}
	transfer     sync.Once
	killOnce     sync.Once
	status       string
	code         int
	failure      error
	start        float64
	now          func() float64
	progress     func(CommandOutput) error
}

// A transferred command remains a real process with pipe readers and a joined
// Wait. It carries no caller scope, Host or prepared Hook decision off-owner.
func runAsynchronousCommandProcess(ctx context.Context, r CommandProcessRequest) (CommandOutput, error) {
	if r.Platform == "windows" || r.Platform == "win32" {
		return CommandOutput{}, unsupported("Windows command hook shell producer")
	}
	if r.NowMillis == nil || r.OutputDirectory == "" {
		return CommandOutput{}, errors.New("async command requires actual clock and task output directory")
	}
	cwd := r.CWD
	if stat, err := os.Stat(cwd); err != nil || !stat.IsDir() {
		cwd = r.OriginalCWD
	}
	text := r.Hook.String("command")
	var cmd *exec.Cmd
	if r.Hook.String("shell") == "powershell" {
		if r.ShellResolver == nil {
			return CommandOutput{}, unsupported("actual cached PowerShell path producer")
		}
		path := r.ShellResolver.PowerShell()
		if path == "" {
			return CommandOutput{}, fmt.Errorf("Hook %q has shell: 'powershell' but no PowerShell executable (pwsh or powershell) was found on PATH. Install PowerShell, or remove \"shell\": \"powershell\" to use bash.", text)
		}
		cmd = exec.CommandContext(context.Background(), path, "-NoProfile", "-NonInteractive", "-Command", text)
	} else {
		if prefix := r.Environment["CLAUDE_CODE_SHELL_PREFIX"]; prefix != "" {
			var err error
			text, err = FormatHookShellPrefix(prefix, text)
			if err != nil {
				return CommandOutput{}, err
			}
		}
		cmd = exec.CommandContext(context.Background(), "/bin/sh", "-c", text)
	}
	cmd.Dir = cwd
	env := map[string]string{}
	for key, value := range r.Environment {
		env[key] = value
	}
	env["CLAUDE_PROJECT_DIR"] = r.ProjectRoot
	keys := []string{}
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	cmd.Env = []string{}
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	input, err := r.Input.MarshalJSON()
	if err != nil {
		return CommandOutput{}, err
	}
	cmd.Stdin = strings.NewReader(string(protocol.StringifyValidatedJavaScriptJSON(input)) + "\n")
	p := &nativeAsyncProcess{command: cmd, done: make(chan struct{}), changed: make(chan struct{}, 1), supervised: make(chan struct{}), stopProgress: make(chan struct{}), transferred: make(chan struct{}), status: "running", now: r.NowMillis, start: r.NowMillis(), progress: r.Progress}
	p.output = newAsyncTaskOutput(func() {
		select {
		case p.changed <- struct{}{}:
		default:
		}
	})
	cmd.Stdout = asyncOutputPipe{output: p.output}
	cmd.Stderr = asyncOutputPipe{output: p.output, stderr: true}
	configureHookProcess(cmd)
	if err = cmd.Start(); err != nil {
		return CommandOutput{}, err
	}
	p.output.setPath(filepath.Join(r.OutputDirectory, fmt.Sprintf("hook_%d.output", cmd.Process.Pid)))
	seconds := 600.0
	var selected float64
	if json.Unmarshal(r.Hook.Get("timeout"), &selected) == nil && selected != 0 {
		seconds = selected
	}
	timeout := time.Duration(seconds * float64(time.Second))
	if timeout < 0 {
		timeout = 0
	}
	go p.wait()
	go p.supervise(ctx, timeout)
	configured := hookTruthy(r.Hook.Get("async")) || hookTruthy(r.Hook.Get("asyncRewake"))
	if configured {
		if output, ok, err := p.background(hookTruthy(r.Hook.Get("asyncRewake")), seconds*1000); ok || err != nil {
			return output, err
		}
	}
	initialChecked := false
	for {
		select {
		case <-p.changed:
			if initialChecked {
				continue
			}
			stdout := p.output.inMemoryStdout()
			line, _, _ := strings.Cut(stdout, "\n")
			if !strings.Contains(strings.TrimFunc(line, javaScriptWhitespace), "}") {
				continue
			}
			initialChecked = true
			if raw, isAsync := parseAsyncHeader(stdout); isAsync {
				asyncTimeout := 0.0
				_ = json.Unmarshal(raw, &asyncTimeout)
				// execCommandHook's first-line async promise retains the actual
				// stdout/stderr/output accumulated before ownership transfer.
				// Config-based async returns the distinct empty DTO above.
				captured, captureError := p.output.snapshot()
				if captureError != nil {
					p.Kill()
					p.Cleanup()
					p.Join()
					return CommandOutput{}, captureError
				}
				if output, ok, err := p.background(false, asyncTimeout); ok || err != nil {
					output.Stdout, output.Stderr, output.Output = captured.Stdout, captured.Stderr, captured.Output
					return output, err
				}
			}
		case <-p.done:
			output, _, err := p.Snapshot()
			if ctx.Err() != nil {
				output.Aborted = true
			}
			p.Cleanup()
			p.Join()
			return output, err
		}
	}
}
func (p *nativeAsyncProcess) background(rewake bool, timeout float64) (CommandOutput, bool, error) {
	p.mu.Lock()
	if p.status != "running" {
		p.mu.Unlock()
		return CommandOutput{}, false, nil
	}
	if !rewake {
		p.status = "backgrounded"
	}
	p.mu.Unlock()
	if !rewake {
		if err := p.output.spill(); err != nil {
			p.Kill()
			<-p.done
			p.Cleanup()
			p.Join()
			return CommandOutput{}, false, err
		}
		p.transfer.Do(func() { close(p.transferred) })
	}
	return CommandOutput{Backgrounded: true, AsyncProcess: p, AsyncTimeout: timeout, AsyncRewake: rewake, AsyncProcessID: fmt.Sprintf("async_hook_%d", p.command.Process.Pid)}, true, nil
}
func (p *nativeAsyncProcess) wait() {
	err := p.command.Wait()
	p.output.finish()
	p.mu.Lock()
	if p.status != "killed" {
		p.status = "completed"
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				p.code = exit.ExitCode()
				if p.code < 0 {
					p.code = 1
				}
			} else {
				p.failure = err
			}
		}
	}
	p.mu.Unlock()
	close(p.done)
}
func (p *nativeAsyncProcess) supervise(ctx context.Context, timeout time.Duration) {
	defer close(p.supervised)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	progress := time.NewTicker(time.Second)
	defer progress.Stop()
	abort := ctx.Done()
	deadline := timer.C
	detached := p.transferred
	last := ""
	for {
		select {
		case <-p.stopProgress:
			return
		case <-p.done:
			return
		case <-detached:
			detached = nil
			abort, deadline = nil, nil
		case <-abort:
			abort = nil
			p.mu.Lock()
			backgrounded := p.status == "backgrounded"
			p.mu.Unlock()
			if cause := context.Cause(ctx); !backgrounded && (cause == nil || cause.Error() != "interrupt") {
				p.Kill()
			}
		case <-deadline:
			deadline = nil
			p.mu.Lock()
			backgrounded := p.status == "backgrounded"
			p.mu.Unlock()
			if !backgrounded {
				p.kill(143)
			}
		case <-progress.C:
			p.mu.Lock()
			callback := p.progress
			p.mu.Unlock()
			if callback != nil {
				output, _, _ := p.Snapshot()
				if output.Output != last {
					last = output.Output
					_ = callback(output)
				}
			}
		}
	}
}
func (p *nativeAsyncProcess) Snapshot() (CommandOutput, string, error) {
	p.mu.Lock()
	code, status, failure := p.code, p.status, p.failure
	p.mu.Unlock()
	output, err := p.output.snapshot()
	output.Code = code
	output.DurationMillis = p.now() - p.start
	if failure != nil {
		err = failure
	}
	return output, status, err
}
func (p *nativeAsyncProcess) Done() <-chan struct{} { return p.done }
func (p *nativeAsyncProcess) Kill()                 { p.kill(137) }
func (p *nativeAsyncProcess) kill(code int) {
	p.killOnce.Do(func() {
		p.mu.Lock()
		p.status, p.code = "killed", code
		p.mu.Unlock()
		if p.command.Cancel != nil {
			_ = p.command.Cancel()
		} else if p.command.Process != nil {
			_ = p.command.Process.Kill()
		}
	})
}
func (p *nativeAsyncProcess) Cleanup() {
	p.cleanupOnce.Do(func() { close(p.stopProgress); p.mu.Lock(); p.progress = nil; p.mu.Unlock(); p.output.clear() })
}
func (p *nativeAsyncProcess) Join() { <-p.done; <-p.supervised }
