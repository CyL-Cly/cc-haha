package sdkhost

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstate"
)

func readRunState(t *testing.T, path string) protocol.JSONObject {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var v protocol.JSONObject
	if e := v.UnmarshalJSON([]byte(lines[len(lines)-1])); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestRunStateCleanupLiveGetterAndUndefinedPhase(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	process, path, _ := inputDiagnostics(t, false)
	registry, _ := runtimecleanup.New(owner)
	worker, _ := sessionstate.New(owner, sessionstate.Options{EmitEvents: func() bool { return false }})
	tasks := []BackgroundTask{}
	calls := 0
	h := &Host{options: Options{Owner: owner, Diagnostics: process, RunState: &RunStateOptions{Registry: registry, Worker: worker, GetTasks: func(*cooperative.Scope) ([]BackgroundTask, error) { calls++; return tasks, nil }, IsShuttingDown: func() bool { return false }}}}
	e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		raw, e := chunkIterator(owner, nil)
		if e != nil {
			return e
		}
		input, e := NewPlainStructuredInputWithDiagnostics(owner, raw, process)
		if e != nil {
			return e
		}
		state, e := h.registerRunState(s, input)
		if e != nil {
			return e
		}
		tasks = []BackgroundTask{{Type: "local_agent", Running: true, Background: true}, {Type: "foreground", Running: true}, {Type: "local_agent", Running: true, Background: true}, {Type: "bash", Running: true, Background: true}, {Type: "bash", Running: false, Background: true}}
		if e := worker.Change(s, "requires_action", nil); e != nil {
			return e
		}
		f, e := registry.Start(s)
		if e != nil {
			return e
		}
		if _, e = f.Await(s); e != nil {
			return e
		}
		if state.active || state.phase != nil {
			return errors.New("idle state fabricated phase")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	v := readRunState(t, path)
	if v.String("event") != "run_state_at_shutdown" || string(v.Get("data")) != `{"run_active":false,"worker_status":"requires_action","internal_events_pending":0,"bg_tasks":{"local_agent":2,"bash":1}}` || calls != 1 {
		t.Fatal(string(v.Get("data")), calls)
	}
}
func TestRunStateWholeEpochFlushUpdatesLateWorkerAndShutdownFlag(t *testing.T) {
	for _, shuttingDown := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "shutting_down"}[shuttingDown], func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			process, path, _ := inputDiagnostics(t, false)
			registry, _ := runtimecleanup.New(owner)
			worker, _ := sessionstate.New(owner, sessionstate.Options{EmitEvents: func() bool { return false }})
			shutdown := false
			h := &Host{options: Options{Owner: owner, Diagnostics: process, RunState: &RunStateOptions{Registry: registry, Worker: worker, GetTasks: func(*cooperative.Scope) ([]BackgroundTask, error) { return nil, nil }, IsShuttingDown: func() bool { return shutdown }}}}
			e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
				raw, e := chunkIterator(owner, nil)
				if e != nil {
					return e
				}
				input, e := NewPlainStructuredInputWithDiagnostics(owner, raw, process)
				if e != nil {
					return e
				}
				state, e := h.registerRunState(s, input)
				if e != nil {
					return e
				}
				if e := state.begin(s); e != nil {
					return e
				}
				state.setPhase("draining_commands")
				// Actual queued runtime reaction runs during the one original flush await.
				_, e = owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
					shutdown = shuttingDown
					return worker.Change(next, "requires_action", nil)
				})
				if e != nil {
					return e
				}
				done, e := state.finish(s)
				if e != nil {
					return e
				}
				if !done || state.active || state.phase == nil || *state.phase != "finally_post_flush" {
					return errors.New("whole epoch not finished")
				}
				if done, e = state.finish(s); done || e != nil {
					return errors.New("empty repeated finish added await")
				}
				f, e := registry.Start(s)
				if e != nil {
					return e
				}
				_, e = f.Await(s)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			data := string(readRunState(t, path).Get("data"))
			want := "idle"
			if shuttingDown {
				want = "requires_action"
			}
			if data != `{"run_active":false,"run_phase":"finally_post_flush","worker_status":"`+want+`","internal_events_pending":0,"bg_tasks":{}}` {
				t.Fatal(data)
			}
		})
	}
}
func TestRunStateRejectsActivePollingAndKeepsActualPendingPhase(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	process, path, _ := inputDiagnostics(t, false)
	registry, _ := runtimecleanup.New(owner)
	worker, _ := sessionstate.New(owner, sessionstate.Options{EmitEvents: func() bool { return false }})
	task := BackgroundTask{Type: "local_agent", Running: true, Background: true}
	h := &Host{options: Options{Owner: owner, Diagnostics: process, RunState: &RunStateOptions{Registry: registry, Worker: worker, GetTasks: func(*cooperative.Scope) ([]BackgroundTask, error) { return []BackgroundTask{task}, nil }, IsShuttingDown: func() bool { return false }}}}
	e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		raw, e := chunkIterator(owner, nil)
		if e != nil {
			return e
		}
		input, e := NewPlainStructuredInput(owner, raw)
		if e != nil {
			return e
		}
		state, e := h.registerRunState(s, input)
		if e != nil {
			return e
		}
		if e = state.begin(s); e != nil {
			return e
		}
		done, e := state.finish(s)
		if !done || e != nil || !state.active || *state.phase != "waiting_for_agents" {
			return errors.New("active polling pretended completed")
		}
		f, e := registry.Start(s)
		if e != nil {
			return e
		}
		if _, e = f.Await(s); e != nil {
			return e
		}
		task.Type = "in_process_teammate"
		done, e = state.finish(s)
		if !done || e != nil {
			return errors.New("long lived teammate blocked original exclusion")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if got := string(readRunState(t, path).Get("data")); got != `{"run_active":true,"run_phase":"waiting_for_agents","worker_status":"running","internal_events_pending":0,"bg_tasks":{"local_agent":1}}` {
		t.Fatal(got)
	}
}
func TestRunStateAsyncDateFailureAllowsLaterCleanup(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	process, _, _ := inputDiagnostics(t, true)
	registry, _ := runtimecleanup.New(owner)
	worker, _ := sessionstate.New(owner, sessionstate.Options{EmitEvents: func() bool { return false }})
	later := false
	h := &Host{options: Options{Owner: owner, Diagnostics: process, RunState: &RunStateOptions{Registry: registry, Worker: worker, GetTasks: func(*cooperative.Scope) ([]BackgroundTask, error) { return nil, nil }, IsShuttingDown: func() bool { return false }}}}
	e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		raw, e := chunkIterator(owner, nil)
		if e != nil {
			return e
		}
		input, e := NewPlainStructuredInput(owner, raw)
		if e != nil {
			return e
		}
		if _, e = h.registerRunState(s, input); e != nil {
			return e
		}
		_, e = registry.Register(s, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { later = true; return nil, nil }))
		if e != nil {
			return e
		}
		f, e := registry.Start(s)
		if e != nil || !later {
			return errors.New("async logger error stopped registry map")
		}
		if _, e = f.Await(s); e == nil {
			return errors.New("async logger error swallowed")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestPlainIOFlushAndPendingRequireCanonicalOwner(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	raw, e := chunkIterator(owner, nil)
	if e != nil {
		t.Fatal(e)
	}
	input, e := NewPlainStructuredInput(owner, raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = other.Run(context.Background(), func(s *cooperative.Scope) error {
		if _, e = input.InternalEventsPending(s); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign pending getter")
		}
		if _, e = input.FlushInternalEvents(s); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign flush")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
