package remoteio

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type retryableError struct {
	err   error
	after time.Duration
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// The original serialized uploader owns one in-flight batch plus its bounded
// pending queue. Retried batches return to the front; late arrivals cannot
// overtake them. Closing resolves producers/flush waiters and cancels I/O.
type batchUploader struct {
	s                            *Service
	ctx                          context.Context
	cancel                       context.CancelFunc
	mu                           sync.Mutex
	pending                      []protocol.JSONObject
	maxBatch, maxQueue, maxBytes int
	maximum, jitter              time.Duration
	send                         func(context.Context, []protocol.JSONObject) error
	draining, closed             bool
	pendingAtClose               int
	changed                      chan struct{}
	asyncTail                    chan struct{}
}

// Preserve the call order of fire-and-forget Source producers. In particular,
// a received acknowledgement cannot be overtaken by processing/processed when
// their native goroutines happen to be scheduled in a different order.
func (u *batchUploader) enqueueAsync(items []protocol.JSONObject) {
	u.mu.Lock()
	previous := u.asyncTail
	current := make(chan struct{})
	u.asyncTail = current
	u.mu.Unlock()
	if !u.s.spawn(func() {
		defer close(current)
		if previous != nil {
			select {
			case <-previous:
			case <-u.ctx.Done():
				return
			}
		}
		_ = u.enqueue(u.ctx, items)
	}) {
		close(current)
	}
}

func newBatchUploader(s *Service, batch, queue, bytes int, maximum, jitter time.Duration, send func(context.Context, []protocol.JSONObject) error) *batchUploader {
	return &batchUploader{s: s, ctx: s.ctx, maxBatch: batch, maxQueue: queue, maxBytes: bytes, maximum: maximum, jitter: jitter, send: send, changed: make(chan struct{})}
}
func (u *batchUploader) notify() { close(u.changed); u.changed = make(chan struct{}) }
func (u *batchUploader) enqueue(ctx context.Context, items []protocol.JSONObject) error {
	if len(items) == 0 {
		return nil
	}
	u.mu.Lock()
	for !u.closed && len(u.pending)+len(items) > u.maxQueue {
		changed := u.changed
		u.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		u.mu.Lock()
	}
	if u.closed {
		u.mu.Unlock()
		return nil
	}
	u.pending = append(u.pending, items...)
	if !u.draining {
		u.draining = true
		if !u.s.spawn(u.drain) {
			u.draining = false
		}
	}
	u.notify()
	u.mu.Unlock()
	return nil
}
func (u *batchUploader) drain() {
	defer func() {
		u.mu.Lock()
		u.draining = false
		if !u.closed && len(u.pending) > 0 && u.ctx.Err() == nil {
			u.draining = true
			if !u.s.spawn(u.drain) {
				u.draining = false
			}
		}
		u.notify()
		u.mu.Unlock()
	}()
	failures := 0
	for {
		u.mu.Lock()
		if u.closed || len(u.pending) == 0 {
			u.mu.Unlock()
			return
		}
		count, bytes := 0, 0
		for count < len(u.pending) && count < u.maxBatch {
			n := len(raw(u.pending[count]))
			if count > 0 && u.maxBytes > 0 && bytes+n > u.maxBytes {
				break
			}
			bytes += n
			count++
		}
		batch := append([]protocol.JSONObject(nil), u.pending[:count]...)
		u.pending = u.pending[count:]
		u.notify()
		u.mu.Unlock()
		err := u.send(u.ctx, batch)
		if err == nil {
			failures = 0
			continue
		}
		u.mu.Lock()
		if u.closed {
			u.mu.Unlock()
			return
		}
		u.pending = append(batch, u.pending...)
		u.notify()
		u.mu.Unlock()
		failures++
		delay := time.Duration(500) * time.Millisecond
		for i := 1; i < failures && delay < u.maximum; i++ {
			delay *= 2
		}
		if delay > u.maximum {
			delay = u.maximum
		}
		delay += time.Duration(float64(u.jitter) * u.s.options.Random())
		var retry *retryableError
		if errors.As(err, &retry) && retry.after > delay {
			delay = retry.after
		}
		if u.s.options.Wait(u.ctx, delay) != nil {
			return
		}
	}
}
func (u *batchUploader) flush(ctx context.Context) error {
	u.mu.Lock()
	for !u.closed && (len(u.pending) > 0 || u.draining) {
		changed := u.changed
		u.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		u.mu.Lock()
	}
	u.mu.Unlock()
	return nil
}
func (u *batchUploader) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return u.pendingAtClose
	}
	return len(u.pending)
}
func (u *batchUploader) close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return
	}
	u.closed = true
	if u.cancel != nil {
		u.cancel()
	}
	u.pendingAtClose = len(u.pending)
	u.pending = nil
	u.notify()
}
