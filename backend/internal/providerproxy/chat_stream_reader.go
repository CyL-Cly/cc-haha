package providerproxy

import (
	"context"
	"errors"
	"io"
	"sync"
)

type ChatReaderOptions struct {
	ChatStreamOptions
	CancelUpstream func(any) error
	OnSettled      func()
}

// ChatReader owns the native IO body and keeps the Source's in-band error
// frames. Cancellation closes that body even while its next read is blocked.
type ChatReader struct {
	body                  io.ReadCloser
	processor             *ChatProcessor
	options               ChatReaderOptions
	mu, readMu, ioMu      sync.Mutex
	queue                 [][]byte
	pending               []byte
	done                  bool
	closeOnce, settleOnce sync.Once
	stopContext           func() bool
}

func NewChatReader(ctx context.Context, body io.ReadCloser, model string, options ChatReaderOptions) (*ChatReader, error) {
	if ctx == nil || body == nil {
		return nil, errors.New("Chat stream requires actual context and body")
	}
	r := &ChatReader{body: body, processor: NewChatProcessor(model, options.ChatStreamOptions), options: options}
	r.stopContext = context.AfterFunc(ctx, func() { _ = r.CloseWithReason(context.Cause(ctx)) })
	return r, nil
}
func (r *ChatReader) settle(reason any) {
	r.closeOnce.Do(func() {
		if r.options.CancelUpstream != nil {
			_ = r.options.CancelUpstream(reason)
		} else {
			_ = r.body.Close()
		}
	})
	r.settleOnce.Do(func() {
		if r.options.OnSettled != nil {
			r.options.OnSettled()
		}
	})
	if r.stopContext != nil {
		r.stopContext()
	}
}
func (r *ChatReader) NextFrame() ([]byte, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	for {
		r.mu.Lock()
		if len(r.queue) > 0 {
			frame := r.queue[0]
			r.queue = r.queue[1:]
			r.mu.Unlock()
			return frame, nil
		}
		if r.done {
			r.mu.Unlock()
			return nil, io.EOF
		}
		r.mu.Unlock()
		buffer := make([]byte, 32*1024)
		n, readErr := r.body.Read(buffer)
		r.mu.Lock()
		if r.done {
			r.mu.Unlock()
			return nil, io.EOF
		}
		frames, ended := r.processor.Feed(buffer[:n])
		if !ended && readErr == io.EOF {
			frames = append(frames, r.processor.Finish()...)
			ended = true
		} else if !ended && readErr != nil {
			frames = append(frames, r.processor.Fail(readErr)...)
			ended = true
		}
		r.queue = append(r.queue, frames...)
		r.done = ended
		done := r.done
		r.mu.Unlock()
		if done {
			r.settle(nil)
		}
	}
}
func (r *ChatReader) Read(buffer []byte) (int, error) {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(r.pending) == 0 {
		frame, err := r.NextFrame()
		if err != nil {
			return 0, err
		}
		r.pending = frame
	}
	n := copy(buffer, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}
func (r *ChatReader) CloseWithReason(reason any) error {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return nil
	}
	r.processor.Cancel()
	r.done = true
	r.queue = nil
	r.mu.Unlock()
	r.settle(reason)
	return nil
}
func (r *ChatReader) Close() error { return r.CloseWithReason(nil) }
