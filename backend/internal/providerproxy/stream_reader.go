package providerproxy

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const responsesTerminalCancelReason = "OpenAI Responses terminal event received"

type ResponsesReaderOptions struct {
	ResponsesStreamOptions
	CancelUpstream func(any) error
}

// ResponsesReader owns a standard IO body; native SDK ownership uses the pure
// processor directly. NextFrame returns one original complete encoded frame.
type ResponsesReader struct {
	body        io.ReadCloser
	processor   *ResponsesProcessor
	options     ResponsesReaderOptions
	mu, readMu  sync.Mutex
	queue       [][]byte
	pending     []byte
	failure     error
	done        bool
	cancelOnce  sync.Once
	stopContext func() bool
}

func NewResponsesReader(ctx context.Context, body io.ReadCloser, model string, o ResponsesReaderOptions) (*ResponsesReader, error) {
	if ctx == nil || body == nil {
		return nil, errors.New("Responses stream requires actual context and body")
	}
	r := &ResponsesReader{body: body, processor: NewResponsesProcessor(model, o.ResponsesStreamOptions), options: o}
	r.stopContext = context.AfterFunc(ctx, func() { _ = r.CloseWithReason(context.Cause(ctx)) })
	return r, nil
}
func (r *ResponsesReader) cancelBody(reason any) {
	r.cancelOnce.Do(func() {
		if r.options.CancelUpstream != nil {
			_ = r.options.CancelUpstream(reason)
		} else {
			_ = r.body.Close()
		}
	})
}
func (r *ResponsesReader) finishProducer() {
	r.cancelBody(responsesTerminalCancelReason)
	r.mu.Lock()
	callback := r.settleLocked()
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
	if r.stopContext != nil {
		r.stopContext()
	}
}
func (r *ResponsesReader) settleLocked() func() {
	if r.processor.settled {
		return nil
	}
	r.processor.settled = true
	return r.processor.options.OnSettled
}
func (r *ResponsesReader) NextFrame() ([]byte, error) {
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
		if r.failure != nil {
			failure := r.failure
			r.mu.Unlock()
			return nil, failure
		}
		if r.done {
			r.mu.Unlock()
			return nil, io.EOF
		}
		r.mu.Unlock()
		buffer := make([]byte, 32*1024)
		n, readError := r.body.Read(buffer)
		r.mu.Lock()
		if r.done {
			r.mu.Unlock()
			return nil, io.EOF
		}
		frames, terminal, err := r.processor.Feed(buffer[:n])
		if err == nil && (terminal || readError == io.EOF) {
			var final [][]byte
			final, err = r.processor.Finish()
			frames = append(frames, final...)
		} else if err == nil && readError != nil {
			err = readError
		}
		// In an errored WHATWG controller the pending read receives its first
		// enqueued frame; additional queued frames are discarded by error().
		if err != nil && len(frames) > 1 {
			frames = frames[:1]
		}
		r.queue = append(r.queue, frames...)
		r.failure = err
		r.done = terminal || readError != nil || err != nil
		done := r.done
		r.mu.Unlock()
		if done {
			r.finishProducer()
		}
	}
}
func (r *ResponsesReader) Read(buffer []byte) (int, error) {
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
func (r *ResponsesReader) CloseWithReason(reason any) error {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return nil
	}
	r.processor.cancelled = true
	var onCancel func(any)
	if !r.processor.terminal {
		onCancel = r.processor.options.OnCancel
	}
	r.done = true
	r.queue = nil
	r.mu.Unlock()
	if onCancel != nil {
		onCancel(reason)
	}
	r.cancelBody(reason)
	r.mu.Lock()
	callback := r.settleLocked()
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
	if r.stopContext != nil {
		r.stopContext()
	}
	return nil
}
func (r *ResponsesReader) Close() error { return r.CloseWithReason(nil) }
func CollectResponses(ctx context.Context, body io.ReadCloser, model string, o ResponsesReaderOptions) (protocol.JSONObject, error) {
	reader, err := NewResponsesReader(ctx, body, model, o)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	defer reader.Close()
	collector := NewResponsesCollector(model, o.NowMillis)
	for {
		frame, err := reader.NextFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if err = collector.Feed(frame); err != nil {
			return protocol.JSONObject{}, err
		}
	}
	return collector.Finish()
}
