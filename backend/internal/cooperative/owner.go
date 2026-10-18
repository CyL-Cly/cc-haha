// Package cooperative preserves one owner call stack at explicit I/O and yield
// boundaries. It is an internal primitive, not a goroutine identity sandbox.
package cooperative

import (
	"context"
	"errors"
	"sync"
)

var ErrClosed = errors.New("cooperative owner closed")
var ErrRestricted = errors.New("command is forbidden while the existing continuation is active")
var ErrStop = errors.New("original consumer stopped")

type Class uint8

const (
	Input Class = iota
	Cancel
	Flush
	Read
	StartASK
	Next
	Feed
)

func allowed(class Class) bool { return class >= Input && class <= Read }

type WaitKind uint8

const (
	NetworkOpen WaitKind = iota
	StreamNext
	RetryTimer
	SDKOutput
	TranscriptOpenStat
	TranscriptAppend
)

func known(kind WaitKind) bool { return kind <= TranscriptAppend }

type FrameKind uint8

const (
	FrameFeed FrameKind = iota
	FrameAbort
	FrameFinish
)

type Receipt struct {
	done chan struct{}
	err  error
}

func (receipt *Receipt) Wait() error           { <-receipt.done; return receipt.err }
func (receipt *Receipt) Done() <-chan struct{} { return receipt.done }

type Action uint8

const (
	Resume Action = iota
	Stop
)

type Yield struct {
	once   sync.Once
	done   chan struct{}
	action Action
	err    error
}

func NewYield() *Yield { return &Yield{done: make(chan struct{})} }

// Resolve accepts only immutable control, never envelopes or Source phases.
func (yield *Yield) Resolve(action Action, err error) {
	yield.once.Do(func() { yield.action = action; yield.err = err; close(yield.done) })
}

type command struct {
	class            Class
	run              func(*Scope) error
	receipt          *Receipt
	independent      bool
	sideContinuation *sideQuestionContinuation
	immediate        *ImmediateTicket
}
type Owner struct {
	sideChanged         chan struct{}
	sideContinuations   map[*sideQuestionContinuation]bool
	mu                  sync.Mutex
	queue               []command
	microtasks          []command
	wake                chan struct{}
	done                chan struct{}
	life                context.Context
	cancel              context.CancelCauseFunc
	active, closing     bool
	timers              []*TimerTicket
	immediates          []*ImmediateTicket
	immediateBatch      []*ImmediateTicket
	immediateBatchEnded bool
}
type Scope struct {
	sideContinuation *sideQuestionContinuation // Only the sealed side-query issuer sets this.
	owner            *Owner
	ctx              context.Context
	valid            bool
	frame, callback  int
}

func New() *Owner {
	life, cancel := context.WithCancelCause(context.Background())
	owner := &Owner{wake: make(chan struct{}, 1), done: make(chan struct{}), life: life, cancel: cancel}
	go owner.loop()
	return owner
}
func (owner *Owner) enqueue(ctx context.Context, class Class, run func(*Scope) error, independent ...bool) (*Receipt, error) {
	if ctx == nil || run == nil {
		return nil, errors.New("owner requires a context and original callback")
	}
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if class > StartASK {
		return nil, ErrRestricted
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closing {
		return nil, ErrClosed
	}
	if class == StartASK && owner.active {
		return nil, ErrRestricted
	}
	if class == StartASK {
		owner.active = true
	}
	receipt := &Receipt{done: make(chan struct{})}
	owner.queue = append(owner.queue, command{class: class, run: run, receipt: receipt, independent: len(independent) == 1 && independent[0]})
	select {
	case owner.wake <- struct{}{}:
	default:
	}
	return receipt, nil
}

// Submit runs only named owner callbacks. It never executes on a worker.
func (owner *Owner) Submit(ctx context.Context, class Class, run func(*Scope) error) (*Receipt, error) {
	if !allowed(class) {
		return nil, ErrRestricted
	}
	return owner.enqueue(ctx, class, run)
}

// SubmitIndependent is reserved for an already admitted owner-lifetime job's
// native completion. Ordinary Input/Read callbacks retain ASK cancellation;
// class alone never grants independence. It cannot start ASK/provider frames.
func (owner *Owner) SubmitIndependent(ctx context.Context, class Class, run func(*Scope) error) (*Receipt, error) {
	if !allowed(class) {
		return nil, ErrRestricted
	}
	return owner.enqueue(ctx, class, run, true)
}

// Run admits one original ASK/producer stack. It must be called off-owner.
func (owner *Owner) Run(ctx context.Context, run func(*Scope) error) error {
	receipt, err := owner.enqueue(ctx, StartASK, func(scope *Scope) error {
		// Cancel cause is retained; joining I/O prevents a worker escaping this stack.
		turn, cancel := context.WithCancelCause(ctx)
		stop := context.AfterFunc(owner.life, func() { cancel(context.Cause(owner.life)) })
		defer stop()
		defer cancel(nil)
		scope.ctx = turn
		return run(scope)
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}
func (owner *Owner) take() (command, bool) {
	return owner.takeQueued(false)
}

// A parked callback may be the owner's idle stack. A StartASK admitted while
// it waits remains queued for loop, which can enter the model stack only after
// that callback returns. Allowed callbacks retain their relative FIFO order.
func (owner *Owner) takeCallback() (command, bool) {
	return owner.takeQueued(true)
}

func (owner *Owner) takeQueued(callbackOnly bool) (command, bool) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if len(owner.microtasks) > 0 {
		next := owner.microtasks[0]
		owner.microtasks[0] = command{}
		owner.microtasks = owner.microtasks[1:]
		return next, true
	}
	if next, ok := owner.takeImmediateLocked(); ok {
		return next, true
	}
	for index, next := range owner.queue {
		if callbackOnly && !allowed(next.class) {
			continue
		}
		if index == 0 {
			owner.queue[0] = command{}
			owner.queue = owner.queue[1:]
			return next, true
		}
		copy(owner.queue[index:], owner.queue[index+1:])
		owner.queue[len(owner.queue)-1] = command{}
		owner.queue = owner.queue[:len(owner.queue)-1]
		return next, true
	}
	return command{}, false
}
func finish(next command, err error) {
	if next.immediate != nil {
		next.immediate.active = false
		next.immediate.callback = nil
	}
	next.receipt.err = err
	close(next.receipt.done)
}
func (owner *Owner) loop() {
	defer close(owner.done)
	defer owner.closeTimers()
	defer owner.closeImmediates()
	for {
		if next, ok := owner.take(); ok {
			if owner.life.Err() != nil && next.sideContinuation == nil {
				finish(next, ErrClosed)
				continue
			}
			scope := &Scope{owner: owner, ctx: owner.life, valid: true}
			if next.class == StartASK {
				owner.mu.Lock()
				owner.active = true
				owner.mu.Unlock()
			}
			var err error
			if next.sideContinuation != nil {
				err = next.sideContinuation.advance()
			} else if next.class == StartASK {
				err = next.run(scope)
			} else {
				err = scope.Inline(next.class, next.run)
			}
			scope.valid = false
			if next.class == StartASK {
				owner.mu.Lock()
				owner.active = false
				owner.mu.Unlock()
			}
			finish(next, err)
			continue
		}
		if owner.life.Err() != nil {
			owner.mu.Lock()
			pending := len(owner.sideContinuations) > 0
			owner.mu.Unlock()
			if !pending {
				return
			}
			<-owner.wake
			continue
		}
		scope := &Scope{owner: owner, ctx: owner.life, valid: true}
		fired := scope.beginImmediateBatch()
		if !fired {
			fired = scope.fireDueTimers()
		}
		scope.valid = false
		if fired {
			continue
		}
		select {
		case <-owner.wake:
		case <-owner.life.Done():
			continue
		}
	}
}

// BelongsTo rejects a scope from a different owner before component effects.
// It must still be called on that scope's owner; it is not goroutine detection.
func (scope *Scope) BelongsTo(owner *Owner) bool {
	return scope != nil && scope.valid && scope.owner == owner
}

func (scope *Scope) check() error {
	if scope == nil || !scope.valid {
		return ErrRestricted
	}
	if scope.ctx.Err() != nil {
		return context.Cause(scope.ctx)
	}
	return nil
}

// Inline is the already-owner route. Scope never goes to an I/O worker. A
// retained Scope used on a foreign goroutine is outside this internal contract.
func (scope *Scope) Inline(class Class, run func(*Scope) error) error {
	if err := scope.check(); err != nil {
		return err
	}
	if !allowed(class) || run == nil {
		return ErrRestricted
	}
	scope.callback++
	defer func() { scope.callback-- }()
	return run(scope)
}
func (scope *Scope) Frame(kind FrameKind, run func() error) error {
	if err := scope.check(); err != nil {
		return err
	}
	if kind > FrameFinish || scope.frame != 0 || scope.callback != 0 || scope.sideContinuation != nil && scope.sideContinuation.promptAuthority != nil || run == nil {
		return ErrRestricted
	}
	scope.frame++
	defer func() { scope.frame-- }()
	return run()
}

// pump runs only Input/Cancel/Flush/Read while this exact call stack is parked.
// Events queued before a ready completion run first. This finite FIFO rule is
// not a claim about arbitrary simultaneous Bun promise/timer scheduling.
func (scope *Scope) pump(done <-chan struct{}, cancelled func()) error {
	if scope.sideContinuation != nil {
		return scope.sideQuestionPump(done, cancelled)
	}
	for {
		if next, ok := scope.owner.takeCallback(); ok {
			if scope.owner.life.Err() != nil && next.sideContinuation == nil {
				finish(next, ErrClosed)
				continue
			}
			err := scope.runQueued(next)
			finish(next, err)
			continue
		}
		select {
		case <-done:
			return nil
		default:
		}
		if scope.beginImmediateBatch() || scope.fireDueTimers() {
			continue
		}
		select {
		case <-done:
			return nil
		case <-scope.owner.wake:
		case <-scope.ctx.Done():
			if cancelled == nil {
				return context.Cause(scope.ctx)
			}
			cancelled()
			cancelled = nil
			// I/O workers are abort-aware and must settle. Keep pumping until joined;
			// owner mutations do not escape onto the worker or after Close returns.
			return scope.join(done)
		}
	}
}
func (scope *Scope) join(done <-chan struct{}) error {
	for {
		if next, ok := scope.owner.take(); ok {
			if allowed(next.class) && next.independent {
				finish(next, scope.owner.runCallback(next))
			} else {
				finish(next, context.Cause(scope.ctx))
			}
			continue
		}
		select {
		case <-done:
			return context.Cause(scope.ctx)
		case <-scope.owner.wake:
		}
	}
}

type bytesResult struct {
	value []byte
	err   error
}

// AwaitBytes accepts only immutable bytes and error, not a mutable provider,
// Envelope or callback. Provider SendPlan/Next adapters remain unimplemented.
func (scope *Scope) AwaitBytes(kind WaitKind, work func(context.Context) ([]byte, error)) ([]byte, error) {
	if err := scope.check(); err != nil {
		return nil, err
	}
	if !known(kind) || work == nil {
		return nil, ErrRestricted
	}
	if scope.callback != 0 && (kind == NetworkOpen || kind == StreamNext || kind == RetryTimer) {
		return nil, ErrRestricted
	}
	ctx, cancel := context.WithCancelCause(scope.ctx)
	defer cancel(nil)
	done := make(chan struct{})
	var result bytesResult
	go func() { value, err := work(ctx); result = bytesResult{append([]byte(nil), value...), err}; close(done) }()
	if err := scope.pump(done, func() { cancel(context.Cause(scope.ctx)) }); err != nil {
		return nil, err
	}
	if scope.ctx.Err() != nil {
		return nil, context.Cause(scope.ctx)
	}
	return result.value, result.err
}

// YieldReceipt is called inline at each real Feed/Abort/Finish Emit. Stop
// unwinds that exact continuation before its next allocation or Next call.
func (scope *Scope) YieldReceipt(yield *Yield) error {
	if err := scope.check(); err != nil {
		return err
	}
	if yield == nil || scope.callback != 0 {
		return ErrRestricted
	}
	if err := scope.pump(yield.done, nil); err != nil {
		return err
	}
	if yield.err != nil {
		return yield.err
	}
	if yield.action == Stop {
		return ErrStop
	}
	if yield.action != Resume {
		return ErrRestricted
	}
	return nil
}

// Close is off-owner; abort-aware work is joined before returning. It is not
// QueryEngine shutdown/checkpoint/accounting or an implicit transcript flush.
func (owner *Owner) Close() {
	owner.mu.Lock()
	if !owner.closing {
		owner.closing = true
		owner.cancel(ErrClosed)
	}
	owner.mu.Unlock()
	<-owner.done
}

// Done is the administrative owner termination signal. It never denotes model
// cancellation and exposes no mutable owner state to a waiting worker.
func (owner *Owner) Done() <-chan struct{} { return owner.done }

// runQueued keeps ordinary commands on the current ASK scope. Only explicitly
// admitted independent job completions receive a fresh owner-lifetime scope.
// A command's class never grants independence from model cancellation.
func (scope *Scope) runQueued(next command) error {
	if next.sideContinuation != nil {
		return next.sideContinuation.advance()
	}
	if next.independent {
		return scope.owner.runCallback(next)
	}
	return scope.Inline(next.class, next.run)
}
