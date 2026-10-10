package providerproxy

import (
	"context"
	"sync"
)

// OpenAITerminalAbortBridge links actual cancellation only until a Responses
// terminal event. The Anthropic SDK's later message_stop cleanup must not mark
// the original upstream HTTP 200 trace as a failed fetch.
type OpenAITerminalAbortBridge struct {
	mu       sync.Mutex
	ctx      context.Context
	source   context.Context
	cancel   context.CancelCauseFunc
	stop     func() bool
	terminal bool
}

func NewOpenAITerminalAbortBridge(source context.Context) *OpenAITerminalAbortBridge {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(source))
	b := &OpenAITerminalAbortBridge{ctx: ctx, source: source, cancel: cancel}
	if source.Err() != nil {
		b.Abort(context.Cause(source))
	} else {
		stop := context.AfterFunc(source, func() { b.Abort(context.Cause(source)) })
		b.mu.Lock()
		b.stop = stop
		b.mu.Unlock()
	}
	return b
}
func (b *OpenAITerminalAbortBridge) Context() context.Context { return b.ctx }
func (b *OpenAITerminalAbortBridge) MarkTerminal() {
	b.mu.Lock()
	// context.AfterFunc is scheduled asynchronously in Go. Source abort events
	// have already propagated before a later terminal callback can run.
	if b.source.Err() != nil && b.ctx.Err() == nil {
		b.cancel(context.Cause(b.source))
	}
	b.terminal = true
	stop := b.stop
	b.stop = nil
	b.mu.Unlock()
	if stop != nil {
		stop()
	}
}
func (b *OpenAITerminalAbortBridge) Abort(reason error) {
	b.mu.Lock()
	if !b.terminal && b.ctx.Err() == nil {
		b.cancel(reason)
	}
	stop := b.stop
	b.stop = nil
	b.mu.Unlock()
	if stop != nil {
		stop()
	}
}
func (b *OpenAITerminalAbortBridge) Dispose() {
	b.mu.Lock()
	stop := b.stop
	b.stop = nil
	b.mu.Unlock()
	if stop != nil {
		stop()
	}
}
