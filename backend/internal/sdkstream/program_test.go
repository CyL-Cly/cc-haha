package sdkstream_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkstream"
)

type chunk struct {
	Source           string
	Hex              string
	Gate, CloseAfter bool
}
type operation struct {
	Op, ID, Name, Message, Action, Reason string
	Number                                int
	Value                                 json.RawMessage
	Mutate                                protocol.JSONObject
}
type input struct {
	Name, Layer string
	Chunks      []chunk
	Operations  []operation
	CancelError *originalError
	AbortBody   bool
}
type originalError struct {
	NameValue string `json:"name"`
	Message   string `json:"message"`
}

func (e *originalError) Error() string { return e.Message }
func (e *originalError) Name() string  { return e.NameValue }

type observation struct {
	Results  []map[string]any `json:"results"`
	Timeline []map[string]any `json:"timeline"`
	Final    map[string]any   `json:"final"`
}
type physicalBody struct {
	owner                  *cooperative.Owner
	original               input
	observe                *observation
	pulls, cursor, cancels int
	ended, signal, started bool
	reason                 string
	pending                *ownerasync.OwnerFuture[sdkstream.ReadResult]
	pendingChunk           *chunk
	pullCompletion         *ownerasync.OwnerFuture[struct{}]
	pullReady              map[int]*ownerasync.OwnerFuture[struct{}]
}

func (body *physicalBody) record(phase string, fields map[string]any) {
	fields["phase"] = phase
	body.observe.Timeline = append(body.observe.Timeline, fields)
}
func (body *physicalBody) Next(scope *cooperative.Scope) (*ownerasync.OwnerFuture[sdkstream.ReadResult], error) {
	future, _ := ownerasync.NewOwnerFuture[sdkstream.ReadResult](body.owner)
	if body.ended {
		return future, future.Resolve(scope, sdkstream.ReadResult{Failure: io.EOF})
	}
	if body.pending != nil {
		return nil, cooperative.ErrRestricted
	}
	body.pending = future
	pull := func(next *cooperative.Scope) error {
		body.pullCompletion, _ = ownerasync.NewOwnerFuture[struct{}](body.owner)
		body.pulls++
		number := body.pulls
		body.record("physical-pull-start", map[string]any{"number": number})
		if ready := body.pullReady[number]; ready != nil {
			_ = ready.Resolve(next, struct{}{})
		}
		if body.cursor == len(body.original.Chunks) {
			body.ended = true
			body.record("physical-eof", map[string]any{"number": number})
			body.pending = nil
			_ = body.pullCompletion.Resolve(next, struct{}{})
			return future.Resolve(next, sdkstream.ReadResult{Failure: io.EOF})
		}
		declared := body.original.Chunks[body.cursor]
		body.cursor++
		body.pendingChunk = &declared
		if declared.Gate {
			return nil
		}
		return body.release(next)
	}
	var err error
	if body.pullCompletion == nil {
		if body.started {
			err = pull(scope)
		} else {
			_, err = body.owner.Microtask(cooperative.Read, pull)
		}
	} else {
		_, err = body.pullCompletion.Then(scope, func(*cooperative.Scope, struct{}, error) error {
			_, err := body.owner.Microtask(cooperative.Read, pull)
			return err
		})
	}

	return future, err
}
func (body *physicalBody) release(scope *cooperative.Scope) error {
	if body.pending == nil || body.pendingChunk == nil {
		return cooperative.ErrRestricted
	}
	original := body.pendingChunk
	bytes := []byte(original.Source)
	if original.Hex != "" {
		var err error
		bytes, err = hex.DecodeString(original.Hex)
		if err != nil {
			return err
		}
	}
	body.record("physical-chunk-enqueue", map[string]any{"number": body.pulls, "bytes": len(bytes), "hex": hex.EncodeToString(bytes)})
	pending := body.pending
	body.pending = nil
	body.pendingChunk = nil
	if original.CloseAfter {
		body.ended = true
		body.record("physical-close-after-chunk", map[string]any{"number": body.pulls})
	}
	_ = body.pullCompletion.Resolve(scope, struct{}{})
	return pending.Resolve(scope, sdkstream.ReadResult{Bytes: bytes})
}
func (body *physicalBody) Return(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	future, _ := ownerasync.NewOwnerFuture[struct{}](body.owner)
	if !body.ended {
		body.ended = true
		body.cancels++
		body.record("physical-cancel-start", map[string]any{"count": body.cancels, "reason": map[string]any{"undefined": true}})
		body.record("physical-cancel-fulfilled", map[string]any{"count": body.cancels})
	}
	return future, future.Resolve(scope, struct{}{})
}
func (body *physicalBody) abort(scope *cooperative.Scope, reason string) {
	if body.signal {
		return
	}
	body.signal = true
	body.reason = reason
	body.record("controller-abort", map[string]any{"reason": reason})
	if body.original.AbortBody && !body.ended {
		body.ended = true
		body.record("physical-body-abort-error", map[string]any{})
		if body.pending != nil {
			pending := body.pending
			body.pending = nil
			body.pendingChunk = nil
			_ = pending.Resolve(scope, sdkstream.ReadResult{Failure: &originalError{"AbortError", "original-body-abort"}})
		}
	}
}
func descriptor[T any](result ownerasync.IteratorResult[T], encode func(T) json.RawMessage) string {
	if !result.Defined {
		return `{"done":true}`
	}
	return fmt.Sprintf(`{"value":%s,"done":%v}`, encode(result.Value), result.Done)
}
func names(failure error) (string, string) {
	var original *originalError
	if errors.As(failure, &original) {
		return original.NameValue, original.Message
	}
	var sdk *provider.SDKStreamError
	if errors.As(failure, &sdk) {
		return sdk.Name, sdk.Message
	}
	var cap *sdkstream.CapabilityError
	if errors.As(failure, &cap) {
		return "Error", cap.Boundary
	}
	return "Error", failure.Error()
}
func runProgram[T comparable](t *testing.T, owner *cooperative.Owner, body *physicalBody, initial *ownerasync.OwnerIterator[T], fresh func() (*ownerasync.OwnerIterator[T], error), makeReturn func(json.RawMessage) T, encode func(T) json.RawMessage, mutate func(T, protocol.JSONObject)) observation {
	t.Helper()
	out := body.observe
	var iterator = initial
	promises := map[string]*ownerasync.OwnerFuture[ownerasync.IteratorResult[T]]{}
	observed := map[string]ownerasync.IteratorResult[T]{}
	failures := map[string]error{}
	observedIDs := []string{}
	references := map[any]int{}
	serial := 0
	ref := func(value any) any {
		if parsed, ok := value.(*sdkstream.Value); ok && (parsed == nil || parsed.Object == nil && (len(parsed.Raw) == 0 || parsed.Raw[0] != '[')) {
			return nil
		}
		v := reflect.ValueOf(value)
		if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
			return nil
		}
		if n := references[value]; n != 0 {
			return n
		}
		serial++
		references[value] = serial
		return serial
	}
	errDescriptor := func(failure error) map[string]any {
		name, message := names(failure)
		return map[string]any{"name": name, "message": message, "string": name + ": " + message, "reference": ref(failure)}
	}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		// Native ReadableStream start() has its own fulfillment reaction. It
		// becomes ready during a real consumer await, without a clock/program-name.
		_, err := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { body.started = true; return nil })
		if err != nil {
			return err
		}
		for _, op := range body.original.Operations {
			switch op.Op {
			case "next", "return", "throw":
				body.record("consumer-call", map[string]any{"operation": op.Op, "id": op.ID, "pullCount": body.pulls})
				var future *ownerasync.OwnerFuture[ownerasync.IteratorResult[T]]
				var err error
				switch op.Op {
				case "next":
					future, err = iterator.Next(scope)
				case "return":
					future, err = iterator.Return(scope, makeReturn(op.Value))
				case "throw":
					future, err = iterator.Throw(scope, &originalError{op.Name, op.Message})
				}
				if err != nil {
					return err
				}
				promises[op.ID] = future
				body.record("consumer-call-returned-promise", map[string]any{"id": op.ID, "promise": true, "observed": false, "pullCount": body.pulls})
				_, err = future.Then(scope, func(_ *cooperative.Scope, result ownerasync.IteratorResult[T], failure error) error {
					observedIDs = append(observedIDs, op.ID)
					if failure != nil {
						failures[op.ID] = failure
						body.record("promise-observer-rejected", map[string]any{"id": op.ID, "error": errDescriptor(failure), "pullCount": body.pulls})
						return nil
					}
					if op.Mutate.IsObject() && !result.Done {
						mutate(result.Value, op.Mutate)
					}
					observed[op.ID] = result
					body.record("promise-observer-fulfilled", map[string]any{"id": op.ID, "source": descriptor(result, encode), "valueReference": ref(result.Value), "pullCount": body.pulls})
					return nil
				})
				if err != nil {
					return err
				}
				_, err = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
					_, hasValue := observed[op.ID]
					_, hasError := failures[op.ID]
					body.record("caller-queued-microtask", map[string]any{"id": op.ID, "observed": hasValue || hasError, "pullCount": body.pulls})
					return nil
				})
				if err != nil {
					return err
				}
			case "await":
				value, failure := promises[op.ID].Await(scope)
				result := map[string]any{"id": op.ID, "pullCount": body.pulls, "signalAborted": body.signal}
				if failure != nil {
					result["error"] = errDescriptor(failure)
					result["sameObserverError"] = failure == failures[op.ID]
					out.Results = append(out.Results, result)
					copy := cloneMap(result)
					body.record("consumer-await-rejected", copy)
				} else {
					result["source"] = descriptor(value, encode)
					result["valueReference"] = ref(value.Value)
					result["sameObserverValue"] = observed[op.ID].Value == value.Value
					out.Results = append(out.Results, result)
					body.record("consumer-await-fulfilled", cloneMap(result))
				}
			case "wait-pull":
				future, _ := ownerasync.NewOwnerFuture[struct{}](owner)
				body.pullReady[op.Number] = future
				if body.pulls >= op.Number {
					_ = future.Resolve(scope, struct{}{})
				}
				if _, err := future.Await(scope); err != nil {
					return err
				}
				body.record("external-original-pull-ready", map[string]any{"number": op.Number})
			case "release":
				body.record("external-original-release", map[string]any{"number": op.Number})
				if err := body.release(scope); err != nil {
					return err
				}
			case "abort":
				body.abort(scope, op.Reason)
			case "body-error":
				body.ended = true
				body.record("external-original-body-error", map[string]any{"name": op.Name, "message": op.Message})
				pending := body.pending
				body.pending = nil
				body.pendingChunk = nil
				if err := pending.Resolve(scope, sdkstream.ReadResult{Failure: &originalError{op.Name, op.Message}}); err != nil {
					return err
				}
			case "held-snapshot":
				body.record("held-snapshot", map[string]any{"observed": append([]string{}, observedIDs...), "pullCount": body.pulls, "signalAborted": body.signal})
			case "new-iterator":
				var err error
				iterator, err = fresh()
				if err != nil {
					return err
				}
				body.record("original-new-iterator", map[string]any{})
			case "consume-for-await":
				future, err := iterator.Next(scope)
				if err != nil {
					return err
				}
				value, err := future.Await(scope)
				if err != nil {
					return err
				}
				body.record("actual-for-await-consumer-value", map[string]any{"source": string(encode(value.Value)), "reference": ref(value.Value), "pullCount": body.pulls})
				future, err = iterator.ReturnUndefined(scope)
				if err != nil {
					return err
				}
				_, err = future.Await(scope)
				if err != nil {
					return err
				}
				body.record("actual-for-await-closed", map[string]any{"pullCount": body.pulls, "signalAborted": body.signal})
			default:
				return fmt.Errorf("unknown original op %s", op.Op)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var reason any
	if body.signal {
		reason = body.reason
	}
	out.Final = map[string]any{"pullCount": body.pulls, "cancelCount": body.cancels, "signalAborted": body.signal, "signalReason": reason, "bodyLocked": false, "observedIDs": observedIDs}
	return *out
}
func cloneMap(value map[string]any) map[string]any {
	copy := map[string]any{}
	for k, v := range value {
		copy[k] = v
	}
	return copy
}
func executeProgram(t *testing.T, original input) observation {
	owner := cooperative.New()
	defer owner.Close()
	observation := &observation{Results: []map[string]any{}, Timeline: []map[string]any{}}
	body := &physicalBody{owner: owner, original: original, observe: observation, pullReady: map[int]*ownerasync.OwnerFuture[struct{}]{}}
	stream, err := sdkstream.New(owner, body, protocol.NewSDKSSEDecoder(nil), func(frame protocol.SDKSSEEvent) (*sdkstream.Value, error) { return provider.ParseSDKFrame(frame, nil) }, func(scope *cooperative.Scope) { body.abort(scope, "AbortError: The operation was aborted.") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if original.Layer == "sse" {
		return runProgram(t, owner, body, stream.SSEIterator(), func() (*ownerasync.OwnerIterator[*protocol.SDKSSEEvent], error) {
			return nil, cooperative.ErrRestricted
		}, func(json.RawMessage) *protocol.SDKSSEEvent { return nil }, func(value *protocol.SDKSSEEvent) json.RawMessage {
			if value == nil {
				return json.RawMessage("null")
			}
			raw, _ := json.Marshal(struct {
				Event string   `json:"event"`
				Data  string   `json:"data"`
				Raw   []string `json:"raw"`
			}{value.Event, value.Data, value.Raw})
			return raw
		}, func(*protocol.SDKSSEEvent, protocol.JSONObject) {})
	}
	iterator, err := stream.Iterator()
	if err != nil {
		t.Fatal(err)
	}
	return runProgram(t, owner, body, iterator, stream.Iterator, func(raw json.RawMessage) *sdkstream.Value {
		value, err := sdkstream.ParseValue(raw)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}, func(value *sdkstream.Value) json.RawMessage {
		raw, err := value.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}, func(value *sdkstream.Value, fields protocol.JSONObject) { value.Object.Assign(fields) })
}

// The native error stack and OS process-level unhandled cancellation are not
// portable descriptors. This finite receipt contract projects named errors;
// untouched full Source stdout/stderr remain in the sealed Source recording.
func portable(value any) any {
	switch object := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range object {
			if key != "stack" {
				out[key] = portable(child)
			}
		}
		return out
	case []any:
		out := []any{}
		for _, child := range object {
			out = append(out, portable(child))
		}
		return out
	default:
		return value
	}
}
func TestActualTwentyOneSDKOriginalPrograms(t *testing.T) {
	raw, err := os.ReadFile("testdata/original-source-programs.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input      input
		Observed   map[string]any
		SourceCode int
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, entry := range cases {
		t.Run(entry.Input.Name, func(t *testing.T) {
			if entry.SourceCode != 0 {
				t.Skip("actual Source native cancel-error exits1; no Go equivalence asserted")
			}
			actual := executeProgram(t, entry.Input)
			self := executeProgram(t, entry.Input)
			first, _ := json.Marshal(actual)
			second, _ := json.Marshal(self)
			if string(first) != string(second) {
				t.Fatalf("Go self differs\n%s\n%s", first, second)
			}
			var observed any
			_ = json.Unmarshal(first, &observed)
			source := map[string]any{"results": entry.Observed["results"], "timeline": entry.Observed["timeline"], "final": entry.Observed["final"]}
			expected, _ := json.Marshal(portable(source))
			received, _ := json.Marshal(portable(observed))
			if string(expected) != string(received) {
				t.Fatalf("strict finite original receipt mismatch\nSOURCE %s\nGO     %s", expected, received)
			}
		})
	}
}
