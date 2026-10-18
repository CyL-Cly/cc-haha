package cooperative

import (
	"context"
	"sync/atomic"
)

type sideQuestionContinuation struct {
	executing           atomic.Bool
	authority           *NativeSideQuestionModels
	dreamAuthority      *NativeAutoDreamModels
	permissionAuthority *NativePermissionRequestHooks
	auxiliaryAuthority  *NativeAuxiliaryModels
	insightsAuthority   *NativeInsightsModels
	promptAuthority     *NativePromptContinuations
	owner               *Owner
	resume              chan struct{}
	segment             chan bool // false: suspended at real await; true: stack finalized
	done                chan struct{}
}

func (lane *sideQuestionContinuation) advance() error {
	lane.executing.Store(true)
	lane.resume <- struct{}{}
	finished := <-lane.segment
	lane.executing.Store(false)
	if finished {
		<-lane.done
		lane.owner.mu.Lock()
		delete(lane.owner.sideContinuations, lane)
		lane.owner.sideChangedLocked()
		lane.owner.mu.Unlock()
		if lane.permissionAuthority != nil {
			lane.permissionAuthority.finishTurnCancels()
		}
	}
	return nil
}

// This private command can also finish a canceled continuation during Owner
// shutdown. No public enqueue path can manufacture its tag or bypass Close.
func (lane *sideQuestionContinuation) enqueueResume() {
	owner := lane.owner
	owner.mu.Lock()
	if !owner.sideContinuations[lane] {
		owner.mu.Unlock()
		return
	}
	receipt := &Receipt{done: make(chan struct{})}
	owner.queue = append(owner.queue, command{class: Read, receipt: receipt, independent: true, sideContinuation: lane})
	select {
	case owner.wake <- struct{}{}:
	default:
	}
	owner.mu.Unlock()
}
func (scope *Scope) pauseSideQuestion(done <-chan struct{}) error {
	lane := scope.sideContinuation
	if lane == nil || done == nil {
		return ErrRestricted
	}
	// The readiness observer owns immutable channels only, not the Scope or any
	// query/decoder/factory state. It schedules an existing sealed continuation.
	go func() {
		select {
		case <-done:
		case <-lane.owner.life.Done():
		}
		lane.enqueueResume()
	}()
	lane.segment <- false
	<-lane.resume
	if scope.ctx.Err() != nil {
		return context.Cause(scope.ctx)
	}
	return nil
}
func (scope *Scope) sideQuestionPump(done <-chan struct{}, cancelled func()) error {
	if err := scope.pauseSideQuestion(done); err != nil {
		if cancelled == nil {
			return err
		}
		cancelled()
		// Physical work must settle before this stack can finalize. Administrative
		// cancellation already woke the stack; join I/O without reading on a worker.
		<-done
		return err
	}
	return nil
}
