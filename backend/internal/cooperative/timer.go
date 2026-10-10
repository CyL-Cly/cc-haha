package cooperative

import (
	"context"
	"sort"
	"time"
)

// TimerTicket separates a clock wake from owner policy. The clock worker owns
// only timer/stop/due/done channels; active and callback belong to Owner.
type TimerTicket struct {
	owner    *Owner
	active   bool
	callback func()
	deadline time.Time
	due      chan struct{}
	stop     chan struct{}
	done     chan struct{}
}

func (scope *Scope) ArmTimer(delay time.Duration, callback func()) (*TimerTicket, error) {
	if err := scope.check(); err != nil {
		return nil, err
	}
	if callback == nil {
		return nil, ErrRestricted
	}
	ticket := &TimerTicket{owner: scope.owner, active: true, callback: callback,
		deadline: time.Now().Add(delay),
		due:      make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	scope.owner.timers = append(scope.owner.timers, ticket)
	clock := time.NewTimer(delay)
	// No worker gets the ticket, policy callback, Scope or business state.
	due, stop, done, wake, life := ticket.due, ticket.stop, ticket.done, scope.owner.wake, scope.owner.life
	go func() {
		defer close(done)
		defer clock.Stop()
		select {
		case <-clock.C:
			close(due)
			select {
			case wake <- struct{}{}:
			default:
			}
		case <-stop:
		case <-life.Done():
		}
	}()
	return ticket, nil
}

func (scope *Scope) ClearTimer(ticket *TimerTicket) error {
	// The original finally still owns cleanup after its caller is cancelled.
	// Context cancellation forbids new work, not stopping/joining this resource.
	if scope == nil || !scope.valid || scope.owner == nil {
		return ErrRestricted
	}
	if ticket == nil || ticket.owner != scope.owner {
		return ErrRestricted
	}
	stopTimer(ticket)
	compactTimers(scope.owner)
	return nil
}

func stopTimer(ticket *TimerTicket) {
	if ticket.active {
		ticket.active = false
		close(ticket.stop)
	}
	<-ticket.done
}

func (owner *Owner) closeTimers() {
	for _, ticket := range owner.timers {
		stopTimer(ticket)
	}
	owner.timers = nil
}

func compactTimers(owner *Owner) {
	live := owner.timers[:0]
	for _, ticket := range owner.timers {
		if ticket.active {
			live = append(live, ticket)
		}
	}
	for index := len(live); index < len(owner.timers); index++ {
		owner.timers[index] = nil
	}
	owner.timers = live
}

// Clock notifications do not enter the ordinary FIFO as already-running Cancel
// callbacks. AwaitNative checks fresh native facts before this policy phase.
func (scope *Scope) fireDueTimers() bool {
	fired := false
	tickets := append([]*TimerTicket(nil), scope.owner.timers...)
	sort.SliceStable(tickets, func(a, b int) bool { return tickets[a].deadline.Before(tickets[b].deadline) })
	for _, ticket := range tickets {
		if !ticket.active {
			continue
		}
		// The clock worker wakes a parked owner; it does not decide whether a
		// deadline has elapsed when an owner continuation is already running.
		if time.Now().Before(ticket.deadline) {
			continue
		}
		stopTimer(ticket)
		scope.callback++
		ticket.callback()
		scope.callback--
		fired = true
	}
	compactTimers(scope.owner)
	return fired
}

// NativeBytesOperation is a resource-owned operation. Checkpoint is a bounded
// nonblocking native read/decoder step on owner; WaitReadable merely waits on a
// private native descriptor, never reading bytes or running policy callbacks.
// This API requires an independently proved native adapter; generic work/done
// channels or fixture knowledge cannot implement Checkpoint.
type NativeBytesOperation interface {
	Checkpoint() (value []byte, ready bool, err error)
	WaitReadable(context.Context) error
}

// AwaitNative parks this exact owner continuation. It admits fresh native facts
// before due-timer policy, then returns to the caller's real fetch finally/clear.
// It joins every temporary readiness wait before returning or starting another.
// Arbitrary nested JS promises and cross-operation scheduling remain outside it.
func (scope *Scope) AwaitNative(operation NativeBytesOperation) ([]byte, error) {
	if err := scope.check(); err != nil {
		return nil, err
	}
	if operation == nil || scope.callback != 0 || scope.frame != 0 {
		return nil, ErrRestricted
	}
	var waitDone chan struct{}
	var waitErr error
	var cancelWait context.CancelFunc
	join := func() {
		if waitDone != nil {
			cancelWait()
			<-waitDone
			waitDone = nil
		}
	}
	defer join()
	for {
		if err := scope.check(); err != nil {
			return nil, err
		}
		if scope.sideContinuation == nil {
			if next, ok := scope.owner.takeCallback(); ok {
				err := scope.runQueued(next)
				finish(next, err)
				continue
			}
		}
		value, ready, err := operation.Checkpoint()
		if ready || err != nil {
			return append([]byte(nil), value...), err
		}
		if waitDone != nil {
			select {
			case <-waitDone:
				if waitErr != nil {
					return nil, waitErr
				}
				join()
			default:
			}
		}
		if scope.sideContinuation == nil && (scope.beginImmediateBatch() || scope.fireDueTimers()) {
			continue
		}
		if waitDone == nil {
			waitContext, cancel := context.WithCancel(scope.ctx)
			cancelWait = cancel
			waitDone = make(chan struct{})
			done := waitDone
			go func() { waitErr = operation.WaitReadable(waitContext); close(done) }()
		}
		if scope.sideContinuation != nil {
			if err := scope.pauseSideQuestion(waitDone); err != nil {
				return nil, err
			}
			continue
		}
		select {
		case <-waitDone:
		case <-scope.owner.wake:
		case <-scope.ctx.Done():
			return nil, context.Cause(scope.ctx)
		}
	}
}
