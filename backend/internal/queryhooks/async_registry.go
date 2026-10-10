package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AsyncProcess is the native ShellCommand boundary. Snapshot must not await a
// process: the original registry checks its status before awaiting its result.
type AsyncProcess interface {
	Snapshot() (CommandOutput, string, error)
	Done() <-chan struct{}
	Kill()
	Cleanup()
	Join()
}
type AsyncHook struct {
	ProcessID, HookID, HookName, HookEvent, Command string
	ToolName, PluginID                              string
	StartTime, Timeout                              float64
	Process                                         AsyncProcess
	Rewake                                          bool
	ResponseAttachmentSent                          bool
}
type AsyncRegistryOptions struct {
	Owner      *cooperative.Owner
	NowMillis  func() float64
	Event      func(*cooperative.Scope, AsyncHook, CommandOutput, string) error
	Notify     func(*cooperative.Scope, string) error
	Invalidate func()
}
type AsyncRegistry struct {
	mu       sync.Mutex
	options  AsyncRegistryOptions
	hooks    map[string]*AsyncHook
	order    []string
	retired  []AsyncProcess
	closed   bool
	jobs     sync.WaitGroup
	closeRun sync.Once
	closedCh chan struct{}
	closeErr error
}

func NewAsyncRegistry(options AsyncRegistryOptions) (*AsyncRegistry, error) {
	if options.Owner == nil || options.NowMillis == nil || options.Event == nil || options.Notify == nil || options.Invalidate == nil {
		return nil, errors.New("async Hooks require actual owner, clock, events, notification queue and session environment cache")
	}
	return &AsyncRegistry{options: options, hooks: map[string]*AsyncHook{}, closedCh: make(chan struct{})}, nil
}
func (r *AsyncRegistry) Owns(owner *cooperative.Owner) bool {
	return r != nil && owner == r.options.Owner
}
func (r *AsyncRegistry) Register(scope *cooperative.Scope, hook AsyncHook) error {
	if r == nil || scope == nil || !scope.BelongsTo(r.options.Owner) {
		return cooperative.ErrRestricted
	}
	if hook.Process == nil || hook.ProcessID == "" || hook.HookID == "" {
		return errors.New("async Hook requires the actual transferred process and identity")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		hook.Process.Kill()
		return cooperative.ErrClosed
	}
	hook.StartTime = r.options.NowMillis()
	if hook.Timeout == 0 {
		hook.Timeout = 15000
	}
	if _, exists := r.hooks[hook.ProcessID]; !exists {
		r.order = append(r.order, hook.ProcessID)
	}
	r.hooks[hook.ProcessID] = &hook
	if hook.Rewake {
		r.jobs.Add(1)
	}
	r.mu.Unlock()
	if hook.Rewake {
		go func() {
			defer r.jobs.Done()
			<-hook.Process.Done()
			_ = r.dispatch(context.Background(), func(current *cooperative.Scope) error {
				r.mu.Lock()
				selected := r.hooks[hook.ProcessID] == &hook && !r.closed
				if selected {
					delete(r.hooks, hook.ProcessID)
					r.retireLocked(hook.Process)
				}
				r.mu.Unlock()
				if !selected {
					return nil
				}
				output, _, err := hook.Process.Snapshot()
				if err != nil {
					return err
				}
				hook.Process.Cleanup()
				outcome := "success"
				if output.Code != 0 {
					outcome = "error"
				}
				if err = r.options.Event(current, hook, output, outcome); err != nil {
					return err
				}
				if output.Code == 2 {
					text := output.Stderr
					if text == "" {
						text = output.Stdout
					}
					return r.options.Notify(current, "<system-reminder>\nStop hook blocking error from command \""+hook.HookName+"\": "+text+"\n</system-reminder>")
				}
				return nil
			})
		}()
	}
	return nil
}
func (r *AsyncRegistry) dispatch(ctx context.Context, callback func(*cooperative.Scope) error) error {
	receipt, err := r.options.Owner.SubmitIndependent(ctx, cooperative.Read, callback)
	if err != nil {
		return err
	}
	return receipt.Wait()
}
func (r *AsyncRegistry) Pending(scope *cooperative.Scope) ([]AsyncHook, error) {
	if r == nil || scope == nil || !scope.BelongsTo(r.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := []AsyncHook{}
	for _, id := range r.order {
		if hook := r.hooks[id]; hook != nil && !hook.Rewake && !hook.ResponseAttachmentSent {
			rows = append(rows, *hook)
		}
	}
	return rows, nil
}

// Responses is the original allSettled registry consumer. Each failed native
// snapshot remains pending; one failure must not drop already completed peers.
func (r *AsyncRegistry) Responses(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if r == nil || scope == nil || !scope.BelongsTo(r.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	r.mu.Lock()
	rows := []AsyncHook{}
	for _, id := range r.order {
		if hook := r.hooks[id]; hook != nil && !hook.Rewake {
			rows = append(rows, *hook)
		}
	}
	r.mu.Unlock()
	responses := []protocol.JSONObject{}
	invalidate := false
	for _, hook := range rows {
		output, status, failure := hook.Process.Snapshot()
		if failure != nil {
			continue
		}
		if status != "completed" && status != "killed" {
			continue
		}
		if status == "killed" || hook.ResponseAttachmentSent || strings.TrimFunc(output.Stdout, javaScriptWhitespace) == "" {
			if status == "killed" {
				hook.Process.Cleanup()
			}
			r.remove(hook.ProcessID)
			continue
		}
		response := protocol.NewJSONObject()
		for _, line := range strings.Split(output.Stdout, "\n") {
			line = strings.TrimFunc(line, javaScriptWhitespace)
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var parsed protocol.JSONObject
			if parsed.UnmarshalJSON([]byte(line)) == nil && parsed.IsObject() && parsed.Get("async") == nil {
				response = parsed
				break
			}
		}
		r.mu.Lock()
		if current := r.hooks[hook.ProcessID]; current != nil {
			current.ResponseAttachmentSent = true
		}
		r.mu.Unlock()
		hook.Process.Cleanup()
		outcome := "success"
		if output.Code != 0 {
			outcome = "error"
		}
		if failure = r.options.Event(scope, hook, output, outcome); failure != nil {
			continue
		}
		payload := protocol.NewJSONObject(protocol.JSONField{Name: "processId", Value: commandJSON(hook.ProcessID)}, protocol.JSONField{Name: "response", Value: commandJSON(response)}, protocol.JSONField{Name: "hookName", Value: commandJSON(hook.HookName)}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(hook.HookEvent)}, protocol.JSONField{Name: "stdout", Value: commandJSON(output.Stdout)}, protocol.JSONField{Name: "stderr", Value: commandJSON(output.Stderr)}, protocol.JSONField{Name: "exitCode", Value: commandJSON(output.Code)})
		if hook.ToolName != "" {
			payload.Set("toolName", commandJSON(hook.ToolName))
		}
		if hook.PluginID != "" {
			payload.Set("pluginId", commandJSON(hook.PluginID))
		}
		responses = append(responses, payload)
		r.remove(hook.ProcessID)
		invalidate = invalidate || hook.HookEvent == "SessionStart"
	}
	if invalidate {
		r.options.Invalidate()
	}
	return responses, nil
}
func (r *AsyncRegistry) remove(id string) {
	r.mu.Lock()
	if hook := r.hooks[id]; hook != nil {
		r.retireLocked(hook.Process)
	}
	delete(r.hooks, id)
	for i, key := range r.order {
		if key == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
}

// Retired handles are only native join references, separate from the owner-only
// policy registry. Release them after the last progress callback physically
// joins, so a long-lived desktop session does not retain past Hook contexts.
func (r *AsyncRegistry) retireLocked(process AsyncProcess) {
	r.retired = append(r.retired, process)
	r.jobs.Add(1)
	go func() {
		defer r.jobs.Done()
		process.Join()
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, p := range r.retired {
			if p == process {
				r.retired = append(r.retired[:i], r.retired[i+1:]...)
				break
			}
		}
	}()
}

// Close is called off the owner after the ASK drains. It cancels real processes,
// waits for their pipe readers and descendants, then emits on the same owner.
func (r *AsyncRegistry) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.closeRun.Do(func() {
		go func() {
			r.mu.Lock()
			r.closed = true
			rows := []AsyncHook{}
			for _, id := range r.order {
				if hook := r.hooks[id]; hook != nil {
					rows = append(rows, *hook)
				}
			}
			r.hooks = map[string]*AsyncHook{}
			retired := append([]AsyncProcess(nil), r.retired...)
			r.retired = nil
			r.mu.Unlock()
			for _, hook := range rows {
				_, status, _ := hook.Process.Snapshot()
				if status != "completed" && status != "killed" {
					hook.Process.Kill()
				}
			}
			for _, hook := range rows {
				<-hook.Process.Done()
				hook.Process.Join()
				output, status, err := hook.Process.Snapshot()
				if err == nil {
					outcome := "cancelled"
					if status == "completed" {
						outcome = "success"
						if output.Code != 0 {
							outcome = "error"
						}
					} else {
						output.Code = 1
					}
					hook.Process.Cleanup()
					err = r.dispatch(context.Background(), func(scope *cooperative.Scope) error { return r.options.Event(scope, hook, output, outcome) })
				}
				if r.closeErr == nil {
					r.closeErr = err
				}
			}
			for _, process := range retired {
				process.Join()
			}
			r.jobs.Wait()
			close(r.closedCh)
		}()
	})
	select {
	case <-r.closedCh:
		return r.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func AsyncAttachment(response protocol.JSONObject) protocol.JSONObject {
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("async_hook_response")})
	for _, name := range []string{"processId", "hookName", "hookEvent", "toolName", "response", "stdout", "stderr", "exitCode"} {
		payload.Set(name, response.Get(name))
	}
	return payload
}

func parseAsyncHeader(text string) (json.RawMessage, bool) {
	first, _, _ := strings.Cut(text, "\n")
	first = strings.TrimFunc(first, javaScriptWhitespace)
	if !strings.Contains(first, "}") {
		return nil, false
	}
	var object protocol.JSONObject
	if object.UnmarshalJSON([]byte(first)) == nil && string(object.Get("async")) == "true" {
		return object.Get("asyncTimeout"), true
	}
	return nil, false
}
