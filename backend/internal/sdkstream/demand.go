// Package sdkstream owns the pinned SDK's chunk, SSE and event demand
// continuations. Physical adapters publish immutable bytes; decoder and values
// remain on the one original Owner. This is not a query or model consumer.
package sdkstream

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ReadResult struct {
	Bytes   []byte
	Failure error
}

// Body is the one admitted physical lease. Next admits one demanded read;
// Return closes that lease after preceding demands, never prefetches.
// Implementations return futures from this Owner and give workers no Scope.
type Body interface {
	Next(*cooperative.Scope) (*ownerasync.OwnerFuture[ReadResult], error)
	Return(*cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error)
}

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string { return "SDK stream demand capability: " + e.Boundary }

// Value retains the exact parsed object reference through producer/observer/
// consumer. Raw scalars are immutable; Object mutators are owner-only.
type Value struct {
	Object *protocol.JSONObject
	Raw    json.RawMessage
}

func ParseValue(raw json.RawMessage) (*Value, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid SDK parsed value")
	}
	value := &Value{Raw: append(json.RawMessage(nil), raw...)}
	if bytes.TrimSpace(raw)[0] == '{' {
		var object protocol.JSONObject
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, err
		}
		value.Object = &object
	}
	return value, nil
}

// ReducerJSON preserves parsed-number lexical carriers until the existing
// business coercion reads them. JSON.stringify(null-for-Infinity) must not run
// before the assembler's actual numeric decisions.
func (value *Value) ReducerJSON() ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	if value.Object != nil {
		return value.Object.MarshalJSON()
	}
	return append([]byte(nil), value.Raw...), nil
}
func (value *Value) MarshalJSON() ([]byte, error) {
	raw, err := value.ReducerJSON()
	if err != nil {
		return nil, err
	}
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}

type Stream struct {
	owner    *cooperative.Owner
	body     Body
	decoder  protocol.SDKFrameDecoder
	chunks   *ownerasync.OwnerIterator[[]byte]
	sse      *ownerasync.OwnerIterator[*protocol.SDKSSEEvent]
	consumed bool
	abort    func(*cooperative.Scope)
	parse    func(protocol.SDKSSEEvent) (*Value, error)
	diagnose func(string, any)
}

func New(owner *cooperative.Owner, body Body, decoder protocol.SDKFrameDecoder, parse func(protocol.SDKSSEEvent) (*Value, error), abort func(*cooperative.Scope), diagnose func(string, any)) (*Stream, error) {
	if owner == nil || body == nil || decoder == nil || parse == nil || abort == nil {
		return nil, cooperative.ErrRestricted
	}
	stream := &Stream{owner: owner, body: body, decoder: decoder, parse: parse, abort: abort, diagnose: diagnose}
	var err error
	stream.chunks, err = ownerasync.NewOwnerIterator(owner, stream.chunkStep)
	if err != nil {
		return nil, err
	}
	stream.sse, err = ownerasync.NewOwnerIterator(owner, stream.sseStep)
	return stream, err
}

func (stream *Stream) chunkStep(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[[]byte], request ownerasync.IteratorRequest[[]byte]) error {
	if request.Returning || request.Throwing {
		closing, err := stream.body.Return(scope)
		if err != nil {
			return err
		}
		return ownerasync.AwaitGenerator(scope, iterator, closing, func(next *cooperative.Scope, _ struct{}) error {
			if request.Throwing {
				return iterator.Fail(next, request.Failure)
			}
			return iterator.CompleteReturn(next)
		}, nil)
	}
	return stream.chunkNext(scope, iterator)
}
func (stream *Stream) chunkNext(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[[]byte]) error {
	chunk, ready, failure := stream.decoder.PollChunk()
	if ready {
		return iterator.Yield(scope, chunk, stream.chunkStep)
	}
	if failure != nil {
		if errors.Is(failure, io.EOF) {
			return iterator.Complete(scope)
		}
		return iterator.Fail(scope, failure)
	}
	read, err := stream.body.Next(scope)
	if err != nil {
		return err
	}
	return ownerasync.AwaitGenerator(scope, iterator, read, func(next *cooperative.Scope, result ReadResult) error {
		stream.decoder.AcceptRead(result.Bytes, result.Failure)
		return stream.chunkNext(next, iterator)
	}, nil)
}

func (stream *Stream) sseStep(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[*protocol.SDKSSEEvent], request ownerasync.IteratorRequest[*protocol.SDKSSEEvent]) error {
	if request.Returning || request.Throwing {
		closing, err := stream.chunks.ReturnUndefined(scope)
		if err != nil {
			return err
		}
		return ownerasync.AwaitGenerator(scope, iterator, closing, func(next *cooperative.Scope, _ ownerasync.IteratorResult[[]byte]) error {
			if request.Throwing {
				return iterator.Fail(next, request.Failure)
			}
			return iterator.CompleteReturn(next)
		}, nil)
	}
	return stream.sseNext(scope, iterator)
}
func (stream *Stream) sseNext(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[*protocol.SDKSSEEvent]) error {
	if frame, ready := stream.decoder.PollFrame(); ready {
		return iterator.Yield(scope, &frame, stream.sseStep)
	}
	read, err := stream.chunks.Next(scope)
	if err != nil {
		return err
	}
	return ownerasync.AwaitGenerator(scope, iterator, read, func(next *cooperative.Scope, result ownerasync.IteratorResult[[]byte]) error {
		if result.Done {
			stream.decoder.FlushLines()
			if frame, ready := stream.decoder.PollFrame(); ready {
				return iterator.Yield(next, &frame, stream.sseStep)
			}
			return iterator.Complete(next)
		}
		stream.decoder.DecodeChunk(result.Value)
		return stream.sseNext(next, iterator)
	}, nil)
}

// SSEIterator is the original lower-layer helper; it has no SDK abort finally.
func (stream *Stream) SSEIterator() *ownerasync.OwnerIterator[*protocol.SDKSSEEvent] {
	return stream.sse
}

// Iterator constructs an unstarted SDK generator. Only first Next marks Stream
// consumed. Return before first Next leaves a later iterator usable.
func (stream *Stream) Iterator() (*ownerasync.OwnerIterator[*Value], error) {
	var last protocol.SDKSSEEvent
	var step ownerasync.GeneratorContinuation[*Value]
	var nextPart func(*cooperative.Scope, *ownerasync.OwnerIterator[*Value]) error
	var closeFailure func(*cooperative.Scope, *ownerasync.OwnerIterator[*Value], error) error
	cleanup := func(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[*Value], request ownerasync.IteratorRequest[*Value], failure error) error {
		stream.abort(scope)
		if failure != nil {
			var named interface{ Name() string }
			if errors.As(failure, &named) && named.Name() == "AbortError" {
				return iterator.Complete(scope)
			}
			return iterator.Fail(scope, failure)
		}
		if request.Returning {
			return iterator.CompleteReturn(scope)
		}
		return iterator.Complete(scope)
	}
	closeFailure = func(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[*Value], failure error) error {
		closing, err := stream.sse.ReturnUndefined(scope)
		if err != nil {
			return err
		}
		return ownerasync.AwaitGenerator(scope, iterator, closing, func(next *cooperative.Scope, _ ownerasync.IteratorResult[*protocol.SDKSSEEvent]) error {
			return cleanup(next, iterator, ownerasync.IteratorRequest[*Value]{}, failure)
		}, func(next *cooperative.Scope, _ error) error {
			return cleanup(next, iterator, ownerasync.IteratorRequest[*Value]{}, failure)
		})
	}

	step = func(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[*Value], request ownerasync.IteratorRequest[*Value]) error {
		if request.Returning || request.Throwing {
			if request.Throwing && stream.diagnose != nil {
				stream.diagnose("Could not parse message into JSON:", last.Data)
				stream.diagnose("From chunk:", append([]string(nil), last.Raw...))
			}
			closing, err := stream.sse.ReturnUndefined(scope)
			if err != nil {
				return err
			}
			return ownerasync.AwaitGenerator(scope, iterator, closing, func(next *cooperative.Scope, _ ownerasync.IteratorResult[*protocol.SDKSSEEvent]) error {
				return cleanup(next, iterator, request, request.Failure)
			}, func(next *cooperative.Scope, failure error) error {
				// AsyncIteratorClose preserves an original abrupt throw over close error.
				if request.Throwing {
					failure = request.Failure
				}
				return cleanup(next, iterator, request, failure)
			})
		}
		return nextPart(scope, iterator)
	}
	nextPart = func(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[*Value]) error {
		pending, err := stream.sse.Next(scope)
		if err != nil {
			return err
		}
		return ownerasync.AwaitGenerator(scope, iterator, pending, func(next *cooperative.Scope, result ownerasync.IteratorResult[*protocol.SDKSSEEvent]) error {
			if result.Done {
				return iterator.Complete(next)
			}
			last = *result.Value
			if last.Event == "chunk" {
				if _, bedrock := stream.decoder.(*protocol.BedrockEventDecoder); !bedrock {
					return nextPart(next, iterator)
				}
			}
			switch last.Event {
			case "completion", "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "chunk":
				value, failure := stream.parse(last)
				if failure != nil {
					if stream.diagnose != nil {
						stream.diagnose("Could not parse message into JSON:", last.Data)
						stream.diagnose("From chunk:", append([]string(nil), last.Raw...))
					}
					return closeFailure(next, iterator, failure)
				}
				return iterator.Yield(next, value, step)
			case "error":
				_, failure := stream.parse(last)
				return closeFailure(next, iterator, failure)
			default:
				return nextPart(next, iterator)
			}
		}, func(next *cooperative.Scope, failure error) error {
			return cleanup(next, iterator, ownerasync.IteratorRequest[*Value]{}, failure)
		})
	}
	return ownerasync.NewOwnerIterator(stream.owner, func(scope *cooperative.Scope, iterator *ownerasync.OwnerIterator[*Value], _ ownerasync.IteratorRequest[*Value]) error {
		if stream.consumed {
			return iterator.Fail(scope, &CapabilityError{Boundary: "Cannot iterate over a consumed stream, use `.tee()` to split the stream."})
		}
		stream.consumed = true
		return nextPart(scope, iterator)
	})
}
