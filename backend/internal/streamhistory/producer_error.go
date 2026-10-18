package streamhistory

import "errors"

// YieldThrow is an original async-generator Throw demand received at a live
// yield. It leaves the commit buffer available to the enclosing producer catch.
// A rejected consumer callback is a different path and still closes Feed.
type YieldThrow struct{ Original error }

func (e *YieldThrow) Error() string {
	if e == nil || e.Original == nil {
		return "invalid generator throw"
	}
	return e.Original.Error()
}
func (e *YieldThrow) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Original
}
func isYieldThrow(e error) bool {
	var thrown *YieldThrow
	return errors.As(e, &thrown) && thrown != nil && thrown.Original != nil
}

// FlushProducerError is Source's outer assistantCommitBuffer.flush(), before
// error presentation. It does not manufacture incomplete partial text or parse
// a transport error as an assembler error. Drain consumes the buffer before the
// first yield, exactly like the Source returned array.
func (c *Controller) FlushProducerError(emit Emit) error {
	if c.closed {
		return ErrClosed
	}
	if emit == nil {
		return errors.New("stream history requires an event consumer")
	}
	return c.drain(false, emit)
}

type AttemptState struct {
	CompletedMessages         int
	CrossedSideEffectBoundary bool
	CommittedOutput           bool
	ServerToolUseStarted      bool
	PartialMessage            bool
}

func (c *Controller) AttemptState() AttemptState {
	return AttemptState{CompletedMessages: len(c.created), CrossedSideEffectBoundary: c.crossed, CommittedOutput: c.committed, ServerToolUseStarted: c.serverToolStarted, PartialMessage: c.start != nil && !c.messageStop}
}
func (c *Controller) FlushProducerErrorWithoutToolUse(emit Emit) error {
	if c.closed {
		return ErrClosed
	}
	if emit == nil {
		return errors.New("stream history requires an event consumer")
	}
	return c.drain(true, emit)
}
