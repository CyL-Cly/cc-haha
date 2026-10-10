package provider

import (
	"context"
	"errors"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkstream"
)

func (binding *openAIStreamBinding) options() providerproxy.ResponsesStreamOptions {
	if binding.grok {
		return providerproxy.ResponsesStreamOptions{NowMillis: binding.nowMillis}
	}
	return providerproxy.ResponsesStreamOptions{OpenAICodexOAuth: true, NowMillis: binding.nowMillis, OnTerminal: func(string) { binding.bridge.MarkTerminal() }, OnCancel: func(reason any) {
		failure, ok := reason.(error)
		if !ok {
			failure = errors.New("The operation was aborted.")
		}
		binding.bridge.Abort(failure)
	}, OnSettled: binding.bridge.Dispose}
}
func (binding *openAIStreamBinding) reader(ctx context.Context, body io.ReadCloser) (*providerproxy.ResponsesReader, error) {
	return providerproxy.NewResponsesReader(ctx, body, binding.model, providerproxy.ResponsesReaderOptions{ResponsesStreamOptions: binding.options(), CancelUpstream: func(any) error { return body.Close() }})
}

// The Source ReadableStream.start pump eagerly reads its upstream body. Each
// physical read produces only immutable bytes; its continuation maps Responses
// and publishes frames on the canonical Owner, independently of SDK demand.
type ownedOpenAIBody struct {
	owner                              *cooperative.Owner
	source                             *physicalDemandBody
	processor                          *providerproxy.ResponsesProcessor
	queue                              [][]byte
	waiter                             *ownerasync.OwnerFuture[sdkstream.ReadResult]
	closing                            *ownerasync.OwnerFuture[struct{}]
	failure                            error
	reading, ending, cancelled, closed bool
}

func newOwnedOpenAIBody(owner *cooperative.Owner, source *physicalDemandBody, binding *openAIStreamBinding) *ownedOpenAIBody {
	return &ownedOpenAIBody{owner: owner, source: source, processor: providerproxy.NewResponsesProcessor(binding.model, binding.options())}
}
func (body *ownedOpenAIBody) start(scope *cooperative.Scope) error { return body.readNext(scope) }
func (body *ownedOpenAIBody) readNext(scope *cooperative.Scope) error {
	if body.ending || body.cancelled {
		return body.finish(scope)
	}
	body.reading = true
	read, err := body.source.Next(scope)
	if err != nil {
		body.reading = false
		body.failure = err
		return body.finish(scope)
	}
	_, err = read.Then(scope, func(next *cooperative.Scope, result sdkstream.ReadResult, failure error) error {
		body.reading = false
		if body.cancelled {
			return body.finish(next)
		}
		if failure != nil {
			result.Failure = failure
		}
		frames, terminal, err := body.processor.Feed(result.Bytes)
		if err == nil && (terminal || errors.Is(result.Failure, io.EOF)) {
			var final [][]byte
			final, err = body.processor.Finish()
			frames = append(frames, final...)
		} else if err == nil && result.Failure != nil {
			err = result.Failure
		}
		if err != nil {
			// WHATWG controller.error clears queued frames. A pending read can
			// already have received the first enqueue in this same continuation.
			body.queue = nil
			if body.waiter != nil && len(frames) > 0 {
				frames = frames[:1]
			} else {
				frames = nil
			}
		}
		body.queue = append(body.queue, frames...)
		body.failure = err
		body.ending = terminal || result.Failure != nil || err != nil
		if err = body.publish(next); err != nil {
			return err
		}
		if body.ending {
			return body.finish(next)
		}
		return body.readNext(next)
	})
	return err
}
func (body *ownedOpenAIBody) publish(scope *cooperative.Scope) error {
	if body.waiter == nil {
		return nil
	}
	result, ready := body.poll()
	if !ready {
		return nil
	}
	waiter := body.waiter
	body.waiter = nil
	return waiter.Resolve(scope, result)
}
func (body *ownedOpenAIBody) poll() (sdkstream.ReadResult, bool) {
	if len(body.queue) > 0 {
		frame := body.queue[0]
		body.queue = body.queue[1:]
		return sdkstream.ReadResult{Bytes: frame}, true
	}
	if body.failure != nil {
		return sdkstream.ReadResult{Failure: body.failure}, true
	}
	if body.ending || body.cancelled {
		return sdkstream.ReadResult{Failure: io.EOF}, true
	}
	return sdkstream.ReadResult{}, false
}
func (body *ownedOpenAIBody) Next(scope *cooperative.Scope) (*ownerasync.OwnerFuture[sdkstream.ReadResult], error) {
	if !scope.BelongsTo(body.owner) || body.waiter != nil {
		return nil, cooperative.ErrRestricted
	}
	future, err := ownerasync.NewOwnerFuture[sdkstream.ReadResult](body.owner)
	if err != nil {
		return nil, err
	}
	if result, ready := body.poll(); ready {
		err = future.Resolve(scope, result)
	} else {
		body.waiter = future
	}
	return future, err
}
func (body *ownedOpenAIBody) finish(scope *cooperative.Scope) error {
	if body.closed || body.reading {
		return nil
	}
	body.closed = true
	if body.closing == nil {
		body.closing, _ = ownerasync.NewOwnerFuture[struct{}](body.owner)
	}
	closing, err := body.source.Return(scope)
	if err != nil {
		return err
	}
	_, err = closing.Then(scope, func(next *cooperative.Scope, _ struct{}, _ error) error {
		// Source reader.cancel().catch discards the physical close failure.
		body.processor.Settled()
		if err := body.publish(next); err != nil {
			return err
		}
		return body.closing.Resolve(next, struct{}{})
	})
	return err
}
func (body *ownedOpenAIBody) Return(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	if !scope.BelongsTo(body.owner) {
		return nil, cooperative.ErrRestricted
	}
	if body.closing == nil {
		body.closing, _ = ownerasync.NewOwnerFuture[struct{}](body.owner)
	}
	body.cancelled = true
	body.queue = nil
	body.processor.Cancel(namedAbort{errors.New("The operation was aborted.")})
	if err := body.publish(scope); err != nil {
		return nil, err
	}
	if err := body.finish(scope); err != nil {
		return nil, err
	}
	return body.closing, nil
}

var _ sdkstream.Body = (*ownedOpenAIBody)(nil)
