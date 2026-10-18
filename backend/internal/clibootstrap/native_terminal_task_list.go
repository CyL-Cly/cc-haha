package clibootstrap

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/fsnotify/fsnotify"
)

// The Source TasksV2Store is shared by REPL, Spinner and footer subscribers.
// The native store owns only its watcher/timers and detached read obligations;
// mutable tasks, list identity and expandedView remain on the existing Owner.
type nativeTerminalTaskListRuntime struct {
	mu          sync.Mutex
	started     bool
	closed      bool
	cancel      context.CancelFunc
	done        chan struct{}
	wake        chan struct{}
	cache       protocol.JSONObject
	listeners   map[uint64]func(json.RawMessage)
	sequence    uint64
	view        string
	viewVersion uint64
}

type nativeTerminalTaskListTarget struct {
	list, directory string
	enabled         bool
	teamsEnabled    bool
	teamContext     protocol.JSONObject
}

func (r *nativeTerminalTaskListRuntime) notify() {
	r.mu.Lock()
	wake, closed := r.wake, r.closed
	r.mu.Unlock()
	if wake != nil && !closed {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (r *nativeTerminalTaskListRuntime) publish(value protocol.JSONObject) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	if r.viewVersion != 0 {
		value.Set("expandedView", quoteMain(r.view))
		value.Set("expandedViewRevision", rawMain(r.viewVersion))
	}
	r.cache = value.Clone()
	listeners := make([]func(json.RawMessage), 0, len(r.listeners))
	for _, listener := range r.listeners {
		listeners = append(listeners, listener)
	}
	r.mu.Unlock()
	for _, listener := range listeners {
		body, _ := value.MarshalJSON()
		listener(body)
	}
}

func (m *MainPrint) initializeNativeTerminalTaskListOnOwner(scope *cooperative.Scope) error {
	if !m.terminalFullscreenBound(scope) || m.taskStore == nil {
		return cooperative.ErrRestricted
	}
	if err := m.Controller.BindNativeTaskEnvironmentOnOwner(scope, m.Startup, m.settings, m.settingState, m.environment); err != nil {
		return err
	}
	r := &m.nativeTerminalTaskList
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return cooperative.ErrClosed
	}
	if r.started {
		return nil
	}
	global, err := m.Global.Get()
	if err != nil {
		return err
	}
	r.view = terminalExpandedView(*global)
	if err = m.Controller.SetTerminalExpandedViewOnOwner(scope, r.view); err != nil {
		return err
	}
	r.cache = protocol.NewJSONObject(protocol.JSONField{Name: "tasks", Value: []byte("null")})
	r.listeners = map[uint64]func(json.RawMessage){}
	r.wake = make(chan struct{}, 1)
	r.started = true
	return nil
}

func (m *MainPrint) SubscribeNativeTerminalTaskList(ctx context.Context, changed func(json.RawMessage)) (json.RawMessage, func(), error) {
	if m == nil || m.Owner == nil || changed == nil {
		return nil, nil, cooperative.ErrRestricted
	}
	var initial json.RawMessage
	var remove func()
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if err := m.initializeNativeTerminalTaskListOnOwner(scope); err != nil {
			return err
		}
		r := &m.nativeTerminalTaskList
		r.mu.Lock()
		r.sequence++
		id := r.sequence
		r.listeners[id] = changed
		initial, _ = r.cache.MarshalJSON()
		if r.done == nil {
			r.done = make(chan struct{})
			workerCtx, cancel := context.WithCancel(context.Background())
			r.cancel = cancel
			go m.runNativeTerminalTaskList(workerCtx, r)
		}
		r.mu.Unlock()
		var once sync.Once
		remove = func() { once.Do(func() { r.mu.Lock(); delete(r.listeners, id); r.mu.Unlock(); r.notify() }) }
		r.notify()
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return initial, remove, err
}

func (m *MainPrint) nativeTerminalTaskListTargetOnOwner(scope *cooperative.Scope) (nativeTerminalTaskListTarget, error) {
	if !m.terminalFullscreenBound(scope) || m.taskStore == nil {
		return nativeTerminalTaskListTarget{}, cooperative.ErrRestricted
	}
	// PromptInput still mounts in actual pane MAIN, but its team-context hook
	// disables the persistent list for non-leaders. No worker list is reset.
	value := nativeTerminalTaskListTarget{enabled: m.teamWorker == nil}
	var err error
	value.teamsEnabled, err = m.nativeSendMessageEnabled(scope)
	if err != nil {
		return value, err
	}
	if !value.enabled {
		return value, nil
	}
	value.list, err = m.Controller.TaskListIDOnOwner(scope, m.options.AgentID)
	if err != nil {
		return value, err
	}
	value.directory = m.taskStore.Directory(value.list)
	if m.teamState != nil {
		value.teamContext = m.teamState.snapshot.Clone()
	}
	return value, nil
}

func (m *MainPrint) readNativeTerminalTaskList(ctx context.Context, resetList string) (nativeTerminalTaskListTarget, []protocol.JSONObject, bool, error) {
	var target nativeTerminalTaskListTarget
	var future *ownerasync.OwnerFuture[[]protocol.JSONObject]
	reset := false
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var err error
		target, err = m.nativeTerminalTaskListTargetOnOwner(scope)
		if err != nil || !target.enabled || resetList != "" && target.list != resetList {
			return err
		}
		future, err = nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
			values, err := m.taskStore.List(target.list)
			if err != nil {
				return nil, err
			}
			if resetList != "" {
				allComplete := len(values) > 0
				for _, task := range values {
					allComplete = allComplete && task.String("status") == "completed"
				}
				if allComplete {
					if err = m.taskStore.Reset(target.list); err != nil {
						return nil, err
					}
					reset = true
					return []protocol.JSONObject{}, nil
				}
			}
			return values, nil
		})
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil || future == nil {
		return target, nil, false, err
	}
	values, err := future.Wait(ctx)
	if ctx.Err() != nil {
		// Cancellation stops the reader's consumer, then joins the genuine
		// finite filesystem obligation while this same Owner remains alive.
		_, _ = future.Wait(context.Background())
	}
	return target, values, reset, err
}

func nativeTerminalTaskListValue(target nativeTerminalTaskListTarget, values []protocol.JSONObject, hidden bool) protocol.JSONObject {
	var tasks json.RawMessage = []byte("null")
	if target.enabled && !hidden {
		tasks = rawMain(values)
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tasks", Value: tasks}, protocol.JSONField{Name: "listId", Value: quoteMain(target.list)}, protocol.JSONField{Name: "teamsEnabled", Value: rawMain(target.teamsEnabled)}, protocol.JSONField{Name: "teamContext", Value: rawMain(target.teamContext)})
}

func (m *MainPrint) runNativeTerminalTaskList(ctx context.Context, r *nativeTerminalTaskListRuntime) {
	defer close(r.done)
	unsubscribe := m.taskStore.SubscribeUpdates(r.notify)
	defer unsubscribe()
	var watcher *fsnotify.Watcher
	var events <-chan fsnotify.Event
	var errors <-chan error
	directory := ""
	var debounce, poll, hide *time.Timer
	var debounceTick, pollTick, hideTick <-chan time.Time
	hideList := ""
	// Source preserves this store state across empty reads and last-unsubscribe.
	// Completed-only files do not reopen a list that is already hidden.
	hidden := false
	stop := func(timer **time.Timer, tick *<-chan time.Time) {
		if *timer != nil {
			(*timer).Stop()
		}
		*timer, *tick = nil, nil
	}
	stopSource := func() {
		if watcher != nil {
			_ = watcher.Close()
			watcher = nil
		}
		events, errors, directory = nil, nil, ""
		stop(&debounce, &debounceTick)
		stop(&poll, &pollTick)
		stop(&hide, &hideTick)
	}
	defer stopSource()
	active := func() bool { r.mu.Lock(); defer r.mu.Unlock(); return !r.closed && len(r.listeners) > 0 }
	fetch := func(resetList string) {
		if !active() {
			return
		}
		target, values, didReset, err := m.readNativeTerminalTaskList(ctx, resetList)
		if err != nil || ctx.Err() != nil || !active() {
			return
		}
		if resetList != "" && target.list != resetList {
			return
		}
		if target.directory != directory || watcher == nil {
			if watcher != nil {
				_ = watcher.Close()
				watcher = nil
			}
			events, errors = nil, nil
			directory = target.directory
			if target.enabled {
				if native, failure := fsnotify.NewWatcher(); failure == nil {
					if failure = native.Add(directory); failure == nil {
						watcher, events, errors = native, native.Events, native.Errors
					} else {
						_ = native.Close()
					}
				}
			}
		}
		visible := make([]protocol.JSONObject, 0, len(values))
		incomplete := false
		for _, task := range values {
			if nativeSettingsTruthy(mainObj(task.Get("metadata")).Get("_internal")) {
				continue
			}
			visible = append(visible, task.Clone())
			incomplete = incomplete || task.String("status") != "completed"
		}
		if target.enabled && (incomplete || len(visible) == 0) {
			hidden = len(visible) == 0
		}
		if !target.enabled || incomplete || len(visible) == 0 {
			stop(&hide, &hideTick)
		} else if hide == nil && resetList == "" && !hidden {
			hideList = target.list
			hide = time.NewTimer(5 * time.Second)
			hideTick = hide.C
		}
		stop(&poll, &pollTick)
		if incomplete {
			poll = time.NewTimer(5 * time.Second)
			pollTick = poll.C
		}
		if resetList != "" && !didReset {
			// The Source verification includes internal tasks and does not
			// restart its five-second clear timer on a failed verification.
			stop(&hide, &hideTick)
		}
		if didReset {
			hidden = true
		}
		r.publish(nativeTerminalTaskListValue(target, visible, hidden))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
			if !active() {
				stopSource()
				continue
			}
			if directory == "" {
				fetch("")
			} else {
				stop(&debounce, &debounceTick)
				debounce = time.NewTimer(50 * time.Millisecond)
				debounceTick = debounce.C
			}
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			stop(&debounce, &debounceTick)
			debounce = time.NewTimer(50 * time.Millisecond)
			debounceTick = debounce.C
		case _, ok := <-errors:
			if !ok {
				errors = nil
			}
		case <-debounceTick:
			stop(&debounce, &debounceTick)
			fetch("")
		case <-pollTick:
			stop(&poll, &pollTick)
			fetch("")
		case <-hideTick:
			stop(&hide, &hideTick)
			fetch(hideList)
		}
	}
}

func (m *MainPrint) closeNativeTerminalTaskList() {
	r := &m.nativeTerminalTaskList
	r.mu.Lock()
	r.closed = true
	cancel, done := r.cancel, r.done
	r.listeners = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (m *MainPrint) expandNativeTerminalTaskView(scope *cooperative.Scope) error {
	if m == nil || m.Controller == nil || scope == nil {
		return cooperative.ErrRestricted
	}
	// Check the actual caller before the async no-op, too. A child never borrows
	// its parent's Scope: the existing sealed native bridge delivers this
	// callback to that parent's Owner and joins the real receipt.
	if _, err := m.Controller.ExpandedViewOnOwner(scope); err != nil {
		return err
	}
	if m.agentChild && m.detachedConstructed {
		detached := m.options.DetachedAgent
		if detached != nil && detached.teammate != nil {
			// The actual inProcessRunner enters runAgent with isAsync=true.
			// Its direct setAppState is a no-op, including the child-local view;
			// the independent setAppStateForTasks/store channel remains live.
			identity, err := m.Controller.TeammateIdentityOnOwner(scope)
			if err != nil || identity != *detached.teammate {
				return cooperative.ErrRestricted
			}
			return nil
		}
		if detached != nil && detached.authParent != nil && detached.workflowTaskViewParent && m.options.QuerySource == "workflow_agent" && detached.AgentType != "" {
			// Only the real Workflow factory carries this callback. Its
			// synchronous runAgent retains the originating direct setter.
			return bridgeAgentVoid(m, detached.authParent, scope, detached.authParent.expandNativeTerminalTaskView)
		}
		if detached != nil && detached.authParent != nil && detached.teammate == nil && (m.options.QuerySource == "agent:custom" || strings.HasPrefix(m.options.QuerySource, "agent:builtin:")) {
			// runAgent shares setAppState only while its ordinary child is in
			// the foreground. Async children have no foreground mode callback;
			// a promoted child reads the actual live foreground-agent flag.
			if detached.isBackgrounded == nil || detached.isBackgrounded() {
				return nil
			}
			return bridgeAgentVoid(m, detached.authParent, scope, detached.authParent.expandNativeTerminalTaskView)
		}
	}
	// The actual TaskCreate/TaskUpdate callsites retain their original order.
	// Initializing this read consumer does not admit another tool invocation.
	if m.options.Interactive && !m.agentChild {
		if err := m.initializeNativeTerminalTaskListOnOwner(scope); err != nil {
			return err
		}
	}
	previous, err := m.Controller.ExpandedViewOnOwner(scope)
	if err != nil {
		return err
	}
	if err = m.Controller.ExpandTaskViewOnOwner(scope); err != nil {
		return err
	}
	if previous != "tasks" && m.options.Interactive && !m.agentChild {
		if err = m.saveNativeTerminalExpandedViewOnOwner(scope, "tasks"); err != nil {
			return err
		}
		r := &m.nativeTerminalTaskList
		r.mu.Lock()
		r.view, r.viewVersion = "tasks", r.viewVersion+1
		value := r.cache.Clone()
		r.mu.Unlock()
		r.publish(value)
	}
	return nil
}
