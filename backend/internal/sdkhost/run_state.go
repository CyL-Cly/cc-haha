package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstate"
	"time"
)

// BackgroundTask is a typed result from the original live task-state getter.
// A caller must derive Running/Background from its actual app state. The host
// cannot fabricate an empty task map from a previously observed idle fixture.
type BackgroundTask struct {
	Type                string
	Running, Background bool
}

// RunStateOptions supplies the actual process cleanup registry and worker state.
// It owns no Agent/controller/input consumer. The current SDK host supports the
// existing bare no-background-work profile; reached active polling remains a
// capability rather than a fabricated timer or a second worker.
type RunStateOptions struct {
	Registry       *runtimecleanup.Registry
	Worker         *sessionstate.State
	GetTasks       func(*cooperative.Scope) ([]BackgroundTask, error)
	IsShuttingDown func() bool
	DrainSDK       func(*cooperative.Scope) error
}

type runState struct {
	host   *Host
	input  *PlainStructuredInput
	active bool
	phase  *string
}

func (h *Host) registerRunState(scope *cooperative.Scope, input *PlainStructuredInput) (*runState, error) {
	options := h.options.RunState
	if options == nil {
		return nil, nil
	}
	if !options.Registry.Owns(h.options.Owner) || !options.Worker.Owns(h.options.Owner) || options.GetTasks == nil || options.IsShuttingDown == nil || h.options.Diagnostics == nil {
		return nil, cap("same-process SDK run-state dependencies")
	}
	state := &runState{host: h, input: input}
	_, err := options.Registry.Register(scope, runtimecleanup.NewCallback(state.startCleanup))
	return state, err
}
func (state *runState) begin(scope *cooperative.Scope) error {
	if state == nil {
		return nil
	}
	state.active = true
	state.phase = nil
	if err := state.host.options.RunState.Worker.Change(scope, "running", nil); err != nil {
		return err
	}
	if drain := state.host.options.RunState.DrainSDK; drain != nil {
		return drain(scope)
	}
	return nil
}
func (state *runState) setPhase(phase string) {
	if state != nil {
		state.phase = &phase
	}
}
func (state *runState) finish(scope *cooperative.Scope) (bool, error) {
	if state == nil || !state.active {
		return false, nil
	}
	// In the actual bare no-tools profile this is the reached empty task branch.
	// Inspect live tasks before entering the original flush, never assume idle.
	tasks, err := state.host.options.RunState.GetTasks(scope)
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if task.Running && task.Background && task.Type != "in_process_teammate" {
			state.setPhase("waiting_for_agents")
			wake := make(chan struct{})
			timer := time.AfterFunc(100*time.Millisecond, func() { close(wake) })
			err := scope.AwaitPromise(wake)
			timer.Stop()
			return true, err
		}
	}
	state.setPhase("finally_flush")
	flushed, err := state.input.FlushInternalEvents(scope)
	if err != nil {
		return false, err
	}
	if _, err = flushed.Await(scope); err != nil {
		return false, err
	}
	state.setPhase("finally_post_flush")
	if !state.host.options.RunState.IsShuttingDown() {
		if err := state.host.options.RunState.Worker.Change(scope, "idle", nil); err != nil {
			return false, err
		}
		if drain := state.host.options.RunState.DrainSDK; drain != nil {
			if err := drain(scope); err != nil {
				return false, err
			}
		}
	}
	state.active = false
	return true, nil
}
func (state *runState) startCleanup(scope *cooperative.Scope) (*runtimecleanup.Future, error) {
	owner := state.host.options.Owner
	if !scope.BelongsTo(owner) {
		return nil, cooperative.ErrRestricted
	}
	result, err := ownerasync.NewOwnerFuture[struct{}](owner)
	if err != nil {
		return nil, err
	}
	failure := state.logCurrent(scope)
	if failure != nil {
		err = result.Reject(scope, failure)
	} else {
		err = result.Resolve(scope, struct{}{})
	}
	if err != nil {
		_ = result.RejectAdministrative(err)
	}
	return result, nil
}
func (state *runState) logCurrent(scope *cooperative.Scope) error {
	tasks, err := state.host.options.RunState.GetTasks(scope)
	if err != nil {
		return err
	}
	grouped := protocol.NewJSONObject()
	counts := map[string]int{}
	for _, task := range tasks {
		if task.Running && task.Background {
			counts[task.Type]++
			grouped.Set(task.Type, diagnostics.Number(float64(counts[task.Type])))
		}
	}
	bg, err := grouped.MarshalJSON()
	if err != nil {
		return err
	}
	worker, err := state.host.options.RunState.Worker.Current(scope)
	if err != nil {
		return err
	}
	pending, err := state.input.InternalEventsPending(scope)
	if err != nil {
		return err
	}
	data := protocol.NewJSONObject(protocol.JSONField{Name: "run_active", Value: raw(state.active)})
	if state.phase != nil {
		data.Set("run_phase", quote(*state.phase))
	}
	data.Set("worker_status", quote(worker))
	data.Set("internal_events_pending", diagnostics.Number(float64(pending)))
	data.Set("bg_tasks", bg)
	return state.host.options.Diagnostics.Log("info", "run_state_at_shutdown", &data)
}
