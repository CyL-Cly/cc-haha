package clibootstrap

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

// Native statusline commands belong to the mounted REPL component and the
// actual process. The native process close signal cancels them before joining
// Host references; component replacement also has its own cancellation signal.
type nativeTerminalStatusLineRuntime struct {
	mu          sync.Mutex
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	jobs        sync.WaitGroup
	next        uint64
	listeners   map[uint64]func()
	rates       protocol.JSONObject
	quotaStatus string // Source currentLimits.status; empty is its initial allowed state.
}

// Register and sample atomically on the settings' existing Owner. Listener
// callbacks only wake their mounted component; they never run another Owner
// operation synchronously in a settings notification.
func (m *MainPrint) SubscribeNativeTerminalStatusLine(ctx context.Context, listener func()) (TerminalStatusLineState, func(), error) {
	var state TerminalStatusLineState
	var id uint64
	runtime := &m.nativeTerminalStatusLine
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		projection, err := m.nativeStatusLineProjection(scope)
		if err != nil {
			return err
		}
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		if runtime.closed {
			return cooperative.ErrClosed
		}
		state = projection.State
		if listener != nil {
			if runtime.listeners == nil {
				runtime.listeners = map[uint64]func(){}
			}
			runtime.next++
			id = runtime.next
			runtime.listeners[id] = listener
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return state, nil, err
	}
	var once sync.Once
	return state, func() { once.Do(func() { runtime.mu.Lock(); delete(runtime.listeners, id); runtime.mu.Unlock() }) }, nil
}

func (m *MainPrint) publishNativeTerminalStatusLineChanged() {
	runtime := &m.nativeTerminalStatusLine
	runtime.mu.Lock()
	listeners := make([]func(), 0, len(runtime.listeners))
	if !runtime.closed {
		for _, listener := range runtime.listeners {
			listeners = append(listeners, listener)
		}
	}
	runtime.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

type TerminalStatusLineState struct {
	Configured        bool
	Command           string
	Padding           float64
	AssistantID       string
	PermissionMode    string
	Model             string
	Trusted           bool
	UserHooksDisabled bool
	EditorMode        string
}

type TerminalStatusLineCache struct {
	AssistantID string
	Exceeds200k bool
	owner       *cooperative.Owner
}

type TerminalStatusLineResult struct {
	State TerminalStatusLineState
	Cache TerminalStatusLineCache
	Text  *string
	Input protocol.JSONObject
}

type TerminalStatusLineInvocation struct {
	State  TerminalStatusLineState
	done   chan struct{}
	result TerminalStatusLineResult
}

// The setting is retained by headless/child sessions as in Source. Only the
// actual interactive main process mounts the component and executes commands.
// Merely declaring a native-looking finite Host cannot supply these bindings.
func (m *MainPrint) nativeStatusLineSettingsBound(scope *cooperative.Scope) bool {
	return m != nil && scope.BelongsTo(m.Owner) && m.options.Native != nil && m.options.Native.NativeProcess && m.Controller != nil && m.Controller.BootstrapOwns(m.Owner) && m.hook != nil && m.hook.HasNativeCommandBindings(m.Owner)
}

func (invocation *TerminalStatusLineInvocation) Wait(ctx context.Context) (TerminalStatusLineResult, error) {
	select {
	case <-invocation.done:
		return invocation.result, nil
	case <-ctx.Done():
		return TerminalStatusLineResult{}, context.Cause(ctx)
	}
}

func (m *MainPrint) TerminalStatusLineState(ctx context.Context) (TerminalStatusLineState, error) {
	var state TerminalStatusLineState
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		value, err := m.nativeStatusLineProjection(scope)
		state = value.State
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return state, err
}

// RunTerminalStatusLine samples the current process/AppState on its Owner,
// then leaves that stack before awaiting the real shell. It cannot consume an
// arbitrary command: the hook comes only from the actual settings producer.
func (m *MainPrint) StartTerminalStatusLine(ctx context.Context, vimMode string, cache *TerminalStatusLineCache, logResult bool) (*TerminalStatusLineInvocation, error) {
	var result TerminalStatusLineResult
	var request queryhooks.CommandProcessRequest
	var reference *ownerasync.NativeReference
	var run context.Context
	var cancel context.CancelFunc
	var stop func() bool
	runtime := &m.nativeTerminalStatusLine
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if cache == nil || cache.owner != nil && cache.owner != m.Owner {
			return cooperative.ErrRestricted
		}
		cache.owner = m.Owner
		projection, err := m.nativeStatusLineProjection(scope)
		if err != nil {
			return err
		}
		result.State = projection.State
		result.Cache, result.Input, err = m.nativeStatusLineInput(scope, projection, vimMode, *cache)
		if err != nil {
			return err
		}
		// This is before the actual shell await, matching the original ref
		// update even when another render cancels an in-flight command.
		*cache = result.Cache
		if projection.Disabled {
			return nil
		}
		if !projection.State.Trusted {
			return m.nativeStatusLineDebugOnOwner(scope, "Skipping StatusLine command execution - workspace trust not accepted", "DEBUG")
		}
		if projection.Hook.String("type") != "command" {
			return nil
		}
		runtime.mu.Lock()
		if runtime.closed {
			runtime.mu.Unlock()
			return cooperative.ErrClosed
		}
		if runtime.ctx == nil {
			runtime.ctx, runtime.cancel = context.WithCancel(context.Background())
		}
		run, cancel = context.WithCancel(ctx)
		stop = context.AfterFunc(runtime.ctx, cancel)
		runtime.jobs.Add(1)
		runtime.mu.Unlock()
		binding, err := m.Controller.BorrowNativeCopyBinding(m.Owner)
		if err == nil {
			reference, err = binding.Acquire(scope)
		}
		if err != nil {
			stop()
			cancel()
			runtime.jobs.Done()
			return err
		}
		environment, err := m.nativeProcessEnvironment(scope)
		if err != nil {
			reference.DeliveryFinished()
			reference = nil
			stop()
			cancel()
			runtime.jobs.Done()
			return err
		}
		state := m.state.Snapshot()
		request = queryhooks.CommandProcessRequest{Hook: projection.Hook.Clone(), Input: result.Input.Clone(), CWD: state.CWD, OriginalCWD: state.OriginalCWD, ProjectRoot: state.ProjectRoot, Platform: m.options.Platform, Environment: environment, NowMillis: m.options.Native.NowMillis, ShellResolver: m.hook.NativeCommandShellResolver()}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return nil, err
	}
	invocation := &TerminalStatusLineInvocation{State: result.State, done: make(chan struct{})}
	if reference == nil {
		invocation.result = result
		close(invocation.done)
		return invocation, nil
	}
	go func() {
		defer close(invocation.done)
		defer runtime.jobs.Done()
		defer reference.DeliveryFinished()
		defer cancel()
		defer stop()
		output, failure := queryhooks.RunCommandProcess(run, request)
		if output.Aborted {
			invocation.result = result
			return
		}
		debug := ""
		level := "DEBUG"
		if failure != nil {
			debug, level = "Status hook failed: "+failure.Error(), "ERROR"
		} else if output.Code == 0 {
			lines := []string{}
			for _, line := range strings.Split(protocol.TrimJavaScriptString(output.Stdout), "\n") {
				if trimmed := protocol.TrimJavaScriptString(line); trimmed != "" {
					lines = append(lines, trimmed)
				}
			}
			if len(lines) > 0 {
				text := strings.Join(lines, "\n")
				result.Text = &text
				if logResult {
					debug = fmt.Sprintf("StatusLine [%s] completed with status %d", request.Hook.String("command"), output.Code)
				}
			}
		} else if logResult {
			debug, level = fmt.Sprintf("StatusLine [%s] completed with status %d", request.Hook.String("command"), output.Code), "WARN"
		}
		if debug != "" {
			read, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { return m.nativeStatusLineDebugOnOwner(scope, debug, level) })
			if err == nil {
				_ = read.Wait()
			}
		}
		invocation.result = result
	}()
	return invocation, nil
}

func (m *MainPrint) RunTerminalStatusLine(ctx context.Context, vimMode string, cache *TerminalStatusLineCache, logResult bool) (TerminalStatusLineResult, error) {
	invocation, err := m.StartTerminalStatusLine(ctx, vimMode, cache, logResult)
	if err != nil {
		return TerminalStatusLineResult{}, err
	}
	return invocation.Wait(context.Background())
}

func (m *MainPrint) nativeStatusLineDebugOnOwner(scope *cooperative.Scope, message, level string) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if err := m.initializeNativeErrorLog(); err != nil {
		return err
	}
	return m.errorLogSink.Debug(message, level)
}

func (m *MainPrint) BeginNativeTerminalStatusLineClose() {
	runtime := &m.nativeTerminalStatusLine
	runtime.mu.Lock()
	runtime.closed = true
	if runtime.cancel != nil {
		runtime.cancel()
	}
	runtime.mu.Unlock()
}

func (m *MainPrint) CloseNativeTerminalStatusLine() {
	m.BeginNativeTerminalStatusLineClose()
	m.nativeTerminalStatusLine.jobs.Wait()
}
