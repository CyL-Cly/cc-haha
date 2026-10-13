package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

type foregroundShellProcess struct {
	shell           *backgroundShell
	mu              sync.Mutex
	background      bool
	byUser          bool
	endOnce         sync.Once
	autoWake        chan struct{}
	done            chan struct{}
	ended           chan struct{}
	totalLines      int64
	outputSize      int64
	outputLarge     bool
	submitInterrupt bool
}

// promote changes the live process's ownership without spawning a second
// command. Timeout/parent listeners stop affecting it once it is backgrounded.
func (p *foregroundShellProcess) promote(byUser bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.background {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
	}
	p.background = true
	p.byUser = byUser
	return true
}
func (p *foregroundShellProcess) cancelForeground(cause error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.background {
		p.shell.cancel(cause)
	}
}
func (p *foregroundShellProcess) isBackground() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.background
}

func (s *Service) foregroundBash(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (json.RawMessage, error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if !s.HasBackgroundTaskBindings() {
		return nil, capability("foreground Bash shared task ownership")
	}
	sandbox, e := s.shellSandbox(scope, in)
	if e != nil {
		return nil, e
	}
	command, e := text(in.Get("command"))
	if e != nil {
		return nil, e
	}
	id, e := taskIdentity('b')
	if e != nil {
		return nil, e
	}
	dir, e := s.backgroundOutputDirectory(scope)
	if e != nil {
		return nil, e
	}
	path := filepath.Join(dir, id+".output")
	environment, e := s.processEnvironment(scope)
	if e != nil {
		return nil, e
	}
	shell, e := s.shellExecutable(scope, environment)
	if e != nil {
		return nil, e
	}
	executable, e := executableInEnvironment(shell, environment)
	if e != nil {
		return nil, e
	}
	timeout := s.shellTimeout(in, environment)
	execution, e := s.prepareBashExecution(ctx, scope, command, id)
	if e != nil {
		return nil, e
	}
	prepareSandboxCommand(&execution, sandbox)
	description := in.String("description")
	if description == "" {
		description = command
	}
	env := []string{}
	keys := []string{}
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+environment[key])
	}
	processContext, cancel := context.WithCancelCause(context.Background())
	physicalLifetime, e := s.options.NativeBinding.Acquire(scope)
	if e != nil {
		cancel(e)
		return nil, e
	}
	prepared, e := native(s, scope, func() (*foregroundShellProcess, error) {
		executionCommand, err := execution.resolveShellContext(processContext, shell)
		if err != nil {
			return nil, err
		}
		if check := s.options.BackgroundTasks.CheckPath; check != nil {
			if e := check(path); e != nil {
				return nil, e
			}
		}
		if e := os.MkdirAll(dir, 0777); e != nil {
			return nil, e
		}
		output, e := openTaskOutput(path)
		if e != nil {
			return nil, e
		}
		cmd := exec.CommandContext(processContext, executable, s.shellArgs(executionCommand)...)
		cmd.Dir = execution.OriginalCwd
		cmd.Env = env
		cmd.Stdout = output
		cmd.Stderr = output
		s.configureShell(cmd)
		if e = cmd.Start(); e != nil {
			_ = output.Close()
			return nil, e
		}
		return &foregroundShellProcess{shell: &backgroundShell{command: cmd, output: output, path: path, ctx: processContext, cancel: cancel, done: make(chan struct{}), finished: make(chan struct{})}, done: make(chan struct{}), ended: make(chan struct{}), autoWake: make(chan struct{})}, nil
	})
	if e != nil {
		cancel(e)
		physicalLifetime.DeliveryFinished()
		return nil, e
	}
	p, e := prepared.Await(scope)
	if e != nil {
		cancel(e)
		go func() {
			defer physicalLifetime.DeliveryFinished()
			if resource, failure := prepared.Wait(context.Background()); failure == nil && resource != nil {
				_ = resource.shell.command.Wait()
				_ = resource.shell.output.Close()
			}
		}()
		return nil, e
	}
	start := s.options.NowMillis()
	// This is the only physical Wait. The background completion handler later
	// joins the same immutable receipt rather than calling Wait a second time.
	completed, e := native(s, scope, func() (processResult, error) {
		defer func() {
			if execution.Path != "" {
				_ = os.Remove(execution.Path)
			}
		}()
		defer physicalLifetime.DeliveryFinished()
		err := p.shell.command.Wait()
		execution.cleanupSandbox()
		_ = p.shell.output.Close()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
				if code < 0 {
					code = 137
				}
			} else {
				close(p.done)
				return processResult{}, err
			}
		}
		if errors.Is(context.Cause(processContext), context.DeadlineExceeded) {
			code = 143
		}
		max := boundedTaskOutputLimit(s.options.Environment["BASH_MAX_OUTPUT_LENGTH"], 30000, 150000)
		f, err := os.Open(path)
		if err != nil {
			close(p.done)
			return processResult{}, err
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(max)))
		if stat, failure := f.Stat(); failure == nil {
			p.outputSize = stat.Size()
			p.outputLarge = stat.Size() > int64(len(data))
		}
		_ = f.Close()
		close(p.done)
		return processResult{Out: string(protocol.DecodeJavaScriptUTF8(data)), Code: code, Interrupted: code == 137, NewCwd: execution.finish()}, err
	})
	if e != nil {
		cancel(e)
		go func() {
			defer physicalLifetime.DeliveryFinished()
			_ = p.shell.command.Wait()
			_ = p.shell.output.Close()
			close(p.done)
		}()
		return nil, e
	}
	p.shell.waited = func() (int, error) { result, e := completed.Wait(context.Background()); return result.Code, e }
	registered := false
	progressCount := 0
	initialThreshold := true
	_, e = completed.Then(scope, func(scope *cooperative.Scope, _ processResult, _ error) error {
		if registered && !p.isBackground() {
			registered = false
			return s.options.BackgroundTasks.Delete(scope, id)
		}
		return nil
	})
	if e != nil {
		cancel(e)
		return nil, e
	}
	shouldAutoBackground, e := s.shellAutoBackgroundAllowed(command)
	if e != nil {
		cancel(e)
		return nil, e
	}
	promoteAutomatic := func(submitInterrupt bool) {
		receipt, e := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
			if !p.promote(false) {
				return nil
			}
			p.mu.Lock()
			p.submitInterrupt = submitInterrupt
			p.mu.Unlock()
			if !registered {
				if e := s.registerForegroundShell(scope, p, completed, id, command, description, call.ToolUseID, true); e != nil {
					return e
				}
				registered = true
			} else if e := s.options.BackgroundTasks.Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
				task = task.Clone()
				task.Set("isBackgrounded", raw(true))
				return task, nil
			}); e != nil {
				return e
			}
			if e := s.startForegroundShellBackground(scope, p, id, description, call.ToolUseID); e != nil {
				return e
			}
			close(p.autoWake)
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			p.shell.cancel(e)
		}
	}
	go func() {
		timer := time.NewTimer(nativeBashTimerDuration(timeout))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			if s.isPowerShell() && context.Cause(ctx) == queryloop.SubmitInterrupt && !skillSimple(s.options.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
				promoteAutomatic(true)
			} else {
				p.cancelForeground(context.Cause(ctx))
			}
		case <-timer.C:
			if !shouldAutoBackground {
				p.cancelForeground(context.DeadlineExceeded)
				return
			}
			promoteAutomatic(false)
		case <-p.done:
		case <-p.ended:
		}
	}()
	defer func() {
		if !p.isBackground() {
			p.cancelForeground(context.Canceled)
			if registered {
				_ = s.options.BackgroundTasks.Delete(scope, id)
			}
			p.endOnce.Do(func() { close(p.ended) })
		}
	}()
	for {
		select {
		case <-p.done:
			result, e := completed.Await(scope)
			if e != nil {
				return nil, e
			}
			if e = s.consumeBashCwd(scope, execution, result.NewCwd, p.isBackground()); e != nil {
				return nil, e
			}
			if p.isBackground() {
				_ = s.options.BackgroundTasks.MarkNotified(scope, id)
			}
			if registered && !p.isBackground() {
				if e = s.options.BackgroundTasks.Delete(scope, id); e != nil {
					return nil, e
				}
				registered = false
			}
			if !p.isBackground() && !p.outputLarge {
				_ = os.Remove(path)
			}
			failed, e := s.shellResultIsError(command, result.Code)
			if e != nil {
				return nil, e
			}
			if s.isPowerShell() || !(failed && ctx.Err() == nil) {
				if e = s.stripProcessHints(scope, &result, command); e != nil {
					return nil, e
				}
			}
			if failed && (ctx.Err() == nil || s.isPowerShell() && context.Cause(ctx) != queryloop.SubmitInterrupt) {
				if s.isPowerShell() {
					return nil, &toolerrors.ShellError{Stdout: result.Out, Stderr: result.Err, Code: result.Code, Interrupted: result.Interrupted}
				}
				return nil, &toolerrors.ShellError{Stderr: result.Out, Code: result.Code, Interrupted: result.Interrupted}
			}
			data := s.shellCompletedData(command, result)
			if p.outputLarge {
				output, e := s.PersistForegroundShellOutput(scope, path, id, p.outputSize)
				if e != nil {
					return nil, e
				}
				if output != "" {
					data.Set("persistedOutputPath", quote(output))
					data.Set("persistedOutputSize", raw(p.outputSize))
				}
			}
			return raw(data), nil
		default:
		}
		interval := time.Second
		if initialThreshold {
			interval = 2 * time.Second
		}
		tick, e := native(s, scope, func() (bool, error) {
			timer := time.NewTimer(interval)
			defer timer.Stop()
			var automatic <-chan struct{}
			if !initialThreshold {
				automatic = p.autoWake
			}
			select {
			case <-p.done:
				return true, nil
			case <-automatic:
				return false, nil
			case <-timer.C:
				return false, nil
			}
		})
		if e != nil {
			return nil, e
		}
		exited, e := tick.Await(scope)
		if e != nil {
			return nil, e
		}
		if exited {
			continue
		}
		if initialThreshold {
			initialThreshold = false
			if !p.isBackground() {
				continue
			}
		}
		if p.isBackground() {
			data := s.shellBackgroundData(command, id)
			if s.isPowerShell() && !p.byUser {
				data.Set("assistantAutoBackgrounded", raw(false))
			}
			p.mu.Lock()
			interrupted := p.submitInterrupt
			p.mu.Unlock()
			if interrupted {
				future, err := native(s, scope, func() (json.RawMessage, error) { return taskTail(path, 150000) })
				if err != nil {
					return nil, err
				}
				output, err := future.Await(scope)
				if err != nil {
					return nil, err
				}
				var value string
				if err := json.Unmarshal(output, &value); err != nil {
					return nil, err
				}
				data.Set("stdout", quote(value))
			}
			stripped, err := s.extractShellHints(scope, data.Get("stdout"), command, !s.isPowerShell())
			if err != nil {
				return nil, err
			}
			data.Set("stdout", stripped)
			if p.byUser {
				data.Set("backgroundedByUser", raw(true))
			}
			return raw(data), nil
		}
		elapsed := math.Floor((s.options.NowMillis() - start) / 1000)
		if s.options.ForegroundShellProgress && !registered && elapsed >= 2 {
			if e = s.registerForegroundShell(scope, p, completed, id, command, description, call.ToolUseID, false); e != nil {
				return nil, e
			}
			registered = true
		}
		tail, e := native(s, scope, func() (foregroundShellProgress, error) { return readForegroundShellProgress(path, p.totalLines) })
		if e != nil {
			return nil, e
		}
		sample, e := tail.Await(scope)
		if e != nil {
			return nil, e
		}
		p.totalLines = sample.lines
		if call.OnProgress != nil {
			kind, prefix := "bash_progress", "bash-progress"
			if s.isPowerShell() {
				kind, prefix = "powershell_progress", "ps-progress"
			}
			value := protocol.NewJSONObject(field("type", kind), field("output", sample.last), field("fullOutput", sample.full), field("elapsedTimeSeconds", elapsed), field("totalLines", sample.lines), field("totalBytes", sample.bytes), field("taskId", id))
			if in.Get("timeout") != nil && timeout != 0 {
				value.Set("timeoutMs", raw(timeout))
			}
			if e = call.OnProgress(quote(fmt.Sprintf("%s-%d", prefix, progressCount)), value); e != nil {
				return nil, e
			}
			progressCount++
		}
	}
}

func (s *Service) registerForegroundShell(scope *cooperative.Scope, p *foregroundShellProcess, completed *ownerasync.OwnerFuture[processResult], id, command, description string, toolID json.RawMessage, background bool) error {
	b := s.options.BackgroundTasks
	data := protocol.NewJSONObject(field("id", id), field("type", "local_bash"), field("status", "running"), field("description", description), field("startTime", s.options.NowMillis()), field("outputFile", p.shell.path), field("outputOffset", 0), field("notified", false), field("command", command), field("completionStatusSentInAttachment", false), field("lastReportedTotalLines", 0), field("isBackgrounded", background))
	if toolID != nil {
		data.Set("toolUseId", toolID)
	}
	if b.AgentID != "" {
		data.Set("agentId", quote(b.AgentID))
	}
	task := &sessioncontroller.BackgroundTask{Data: data, Cancel: p.shell.cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return taskTail(p.shell.path, 8*1024*1024) }, ReadActiveOutput: func(context.Context) (json.RawMessage, error) {
		return taskHead(p.shell.path, s.options.Environment["BASH_MAX_OUTPUT_LENGTH"])
	}, Join: func(ctx context.Context) error {
		if !p.isBackground() {
			_, e := completed.Wait(ctx)
			return e
		}
		select {
		case <-p.shell.finished:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}}
	task.Promote = func(scope *cooperative.Scope) error {
		if e := s.check(scope); e != nil {
			return e
		}
		if !p.promote(true) {
			return nil
		}
		if e := b.Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			task = task.Clone()
			task.Set("isBackgrounded", raw(true))
			return task, nil
		}); e != nil {
			return e
		}
		return s.startForegroundShellBackground(scope, p, id, description, toolID)
	}
	if background {
		task.Promote = nil
	}
	if e := b.Register(scope, task); e != nil {
		return e
	}
	event := protocol.NewJSONObject(field("type", "system"), field("subtype", "task_started"), field("task_id", id), field("description", description), field("task_type", "local_bash"))
	if toolID != nil {
		event.Set("tool_use_id", toolID)
	}
	if b.AgentID == "" {
		return b.Emit(scope, event)
	}
	return nil
}

type foregroundShellProgress struct {
	last, full   string
	lines, bytes int64
}

func readForegroundShellProgress(path string, previous int64) (foregroundShellProgress, error) {
	f, e := os.Open(path)
	if e != nil {
		return foregroundShellProgress{}, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return foregroundShellProgress{}, e
	}
	offset := int64(0)
	if st.Size() > 4096 {
		offset = st.Size() - 4096
	}
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return foregroundShellProgress{}, e
	}
	data, e := io.ReadAll(io.LimitReader(f, 4096))
	if e != nil {
		return foregroundShellProgress{}, e
	}
	content := string(protocol.DecodeJavaScriptUTF8(data))
	if content == "" {
		return foregroundShellProgress{lines: previous, bytes: st.Size()}, nil
	}
	pos, n5, n100, count := len(content), 0, 0, int64(0)
	for pos > 0 {
		pos = strings.LastIndexByte(content[:pos], '\n')
		count++
		if count == 5 {
			if pos > 0 {
				n5 = pos + 1
			}
		}
		if count == 100 {
			if pos > 0 {
				n100 = pos + 1
			}
		}
	}
	lines := count
	if int64(len(data)) < st.Size() {
		lines = int64(math.Floor(float64(st.Size())/float64(len(data))*float64(count) + 0.5))
		if lines < previous {
			lines = previous
		}
	}
	return foregroundShellProgress{content[n5:], content[n100:], lines, st.Size()}, nil
}

func (s *Service) startForegroundShellBackground(scope *cooperative.Scope, p *foregroundShellProcess, id, description string, toolID json.RawMessage) error {
	lease, e := s.options.NativeBinding.Acquire(scope)
	if e != nil {
		return e
	}
	go s.waitBackgroundShell(p.shell, id, description, toolID, lease)
	p.endOnce.Do(func() { close(p.ended) })
	return nil
}
