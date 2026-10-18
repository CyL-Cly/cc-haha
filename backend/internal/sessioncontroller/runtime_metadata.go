package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Desktop launch metadata is written by Source's SessionService independently
// of the CLI's lazy Project pointer. Target the existing transcript through the
// shared Writer queue without materializing that pointer: buffering until the
// first model record loses a confirmed selection on restart before that turn.
func (c *Controller) AppendRuntimeMetadata(scope *cooperative.Scope, metadata protocol.JSONObject) error {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || c.writer.async == nil {
		return cooperative.ErrRestricted
	}
	if metadata.String("type") != "session-meta" {
		return capability("runtime metadata type")
	}
	pending := c.writer.async
	var future *WriteFuture
	err := scope.Inline(cooperative.Read, func(next *cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		c.writer.mu.Lock()
		skipped := c.writer.skipped()
		path := c.writer.path
		if path == "" {
			path = c.writer.target()
		}
		sid := c.writer.currentSessionID()
		c.writer.mu.Unlock()
		future = newWriteFuture()
		if skipped {
			future.settle(nil)
			return nil
		}
		// enqueue opens without O_CREATE. A non-persisted or deleted session
		// stays absent, matching Source's missing-transcript metadata no-op.
		job := &appendContinuation{pending: pending, entry: &metadata, sid: sid, path: path, future: future}
		job.enqueue(next)
		return nil
	})
	if err != nil {
		return err
	}
	if err = future.Await(scope); err != nil {
		return err
	}
	// Source SessionService.appendJsonlEntry owns its open/write/finally-close
	// Promise, not another Project drain. A metadata ACK requires its own physical
	// result after enqueue, even when a different manual drain remains in flight.
	flush, err := pending.beginExplicit(scope)
	if err != nil {
		return err
	}
	return flush.Await(scope)
}
