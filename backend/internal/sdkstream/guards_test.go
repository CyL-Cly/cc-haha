package sdkstream_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkstream"
)

type capabilityBody struct {
	owner                               *cooperative.Owner
	chunks                              [][]byte
	nextError, returnError, errorFuture error
	foreign                             bool
	reads, returns                      int
}

func (body *capabilityBody) Next(scope *cooperative.Scope) (*ownerasync.OwnerFuture[sdkstream.ReadResult], error) {
	body.reads++
	if body.nextError != nil {
		return nil, body.nextError
	}
	future, _ := ownerasync.NewOwnerFuture[sdkstream.ReadResult](body.owner)
	if body.foreign {
		return future, nil
	}
	if body.errorFuture != nil {
		return future, future.Reject(scope, body.errorFuture)
	}
	result := sdkstream.ReadResult{Failure: io.EOF}
	if len(body.chunks) > 0 {
		result.Bytes = body.chunks[0]
		result.Failure = nil
		body.chunks = body.chunks[1:]
	}
	return future, future.Resolve(scope, result)
}
func (body *capabilityBody) Return(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
	body.returns++
	future, _ := ownerasync.NewOwnerFuture[struct{}](body.owner)
	if body.returnError != nil {
		return future, future.Reject(scope, body.returnError)
	}
	return future, future.Resolve(scope, struct{}{})
}
func TestSDKDemandDependencyGuardsAndParsedValueBoundaries(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	body := &capabilityBody{owner: owner}
	decoder := protocol.NewSDKSSEDecoder(nil)
	parse := func(frame protocol.SDKSSEEvent) (*sdkstream.Value, error) { return provider.ParseSDKFrame(frame, nil) }
	abort := func(*cooperative.Scope) {}
	for _, makeInvalid := range []func() (*sdkstream.Stream, error){
		func() (*sdkstream.Stream, error) { return sdkstream.New(nil, body, decoder, parse, abort, nil) },
		func() (*sdkstream.Stream, error) { return sdkstream.New(owner, nil, decoder, parse, abort, nil) },
		func() (*sdkstream.Stream, error) { return sdkstream.New(owner, body, nil, parse, abort, nil) },
		func() (*sdkstream.Stream, error) { return sdkstream.New(owner, body, decoder, nil, abort, nil) },
		func() (*sdkstream.Stream, error) { return sdkstream.New(owner, body, decoder, parse, nil, nil) },
	} {
		if _, err := makeInvalid(); !errors.Is(err, cooperative.ErrRestricted) {
			t.Fatal(err)
		}
	}
	if _, err := sdkstream.ParseValue([]byte(`{broken`)); err == nil {
		t.Fatal("invalid parsed input accepted")
	}
	for _, raw := range []string{`null`, `-0`, `[1,2]`, `"\ud800"`, `1e400`, `{"future":1e400,"__proto__":3,"lone":"\ud800"}`} {
		value, err := sdkstream.ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		reducer, err := value.ReducerJSON()
		if err != nil || string(reducer) != raw {
			t.Fatal("reducer observed premature stringify", string(reducer), raw, err)
		}
		encoded, err := value.MarshalJSON()
		expected, failure := protocol.ParseAndStringifyJavaScriptJSON([]byte(raw))
		if err != nil || failure != nil || string(encoded) != string(expected) {
			t.Fatal(string(encoded), string(expected), err)
		}
	}
	var absent *sdkstream.Value
	raw, err := absent.MarshalJSON()
	if err != nil || string(raw) != "null" {
		t.Fatal(string(raw), err)
	}
	value, _ := sdkstream.ParseValue([]byte(`{}`))
	spaced, _ := sdkstream.ParseValue([]byte(" \n{\"original\":1} \t"))
	if spaced.Object == nil {
		t.Fatal("JSON.parse object with surrounding JSON whitespace lost identity")
	}
	spaced.Object.Set("late", json.RawMessage("true"))
	if raw, err := spaced.MarshalJSON(); err != nil || string(raw) != `{"original":1,"late":true}` {
		t.Fatal(string(raw), err)
	}
	value.Object.Set("invalid", json.RawMessage("bad-json"))
	if _, err := value.MarshalJSON(); err == nil {
		t.Fatal("invalid programmatic field accepted at output boundary")
	}
}
func TestSDKDemandPhysicalAdmissionAndCloseRejectionsPreserveOwnership(t *testing.T) {
	for _, mode := range []string{"next-error", "future-error", "foreign-future", "return-reject", "throw-and-return-reject", "abort-error"} {
		t.Run(mode, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			foreign := cooperative.New()
			defer foreign.Close()
			original := errors.New("original immutable physical capability failure")
			body := &capabilityBody{owner: owner, chunks: [][]byte{[]byte("event: message_start\ndata: {}\n\n")}}
			switch mode {
			case "next-error":
				body.nextError = original
			case "future-error":
				body.errorFuture = original
			case "foreign-future":
				body.owner = foreign
				body.foreign = true
			case "return-reject", "throw-and-return-reject":
				body.returnError = original
			case "abort-error":
				body.errorFuture = &originalError{"AbortError", "native body abort"}
			}
			aborts := 0
			diagnostics := []string{}
			stream, err := sdkstream.New(owner, body, protocol.NewSDKSSEDecoder(nil), func(frame protocol.SDKSSEEvent) (*sdkstream.Value, error) { return provider.ParseSDKFrame(frame, nil) }, func(*cooperative.Scope) { aborts++ }, func(message string, _ any) { diagnostics = append(diagnostics, message) })
			if err != nil {
				t.Fatal(err)
			}
			iterator, _ := stream.Iterator()
			if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				next, err := iterator.Next(scope)
				if err != nil {
					return err
				}
				result, failure := next.Await(scope)
				switch mode {
				case "return-reject":
					closing, err := iterator.ReturnUndefined(scope)
					if err != nil {
						return err
					}
					_, failure = closing.Await(scope)
					if failure != original {
						t.Fatal("close error lost", failure)
					}
				case "throw-and-return-reject":
					throw := &originalError{"Error", "original caller throw"}
					closing, err := iterator.Throw(scope, throw)
					if err != nil {
						return err
					}
					_, failure = closing.Await(scope)
					if failure != throw {
						t.Fatal("close replaced abrupt throw", failure)
					}
				case "abort-error":
					if failure != nil || !result.Done {
						t.Fatal("body AbortError not quiet", result, failure)
					}
				case "foreign-future":
					if !errors.Is(failure, cooperative.ErrRestricted) {
						t.Fatal(failure)
					}
				default:
					if failure != original {
						t.Fatal("physical failure identity lost", failure)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if aborts != 1 || body.reads != 1 {
				t.Fatal("business retried or skipped SDK finally", aborts, body.reads)
			}
			if mode == "throw-and-return-reject" && !reflect.DeepEqual(diagnostics, []string{"Could not parse message into JSON:", "From chunk:"}) {
				t.Fatal(diagnostics)
			}
		})
	}
}
func TestSDKDemandIgnoresUnparsedPingAndPreservesDiagnostics(t *testing.T) {
	for _, wire := range []string{"event: ping\ndata: invalid-json\n\nevent: unknown\ndata: ???\n\nevent: message_stop\ndata: {}\n\n", "event: message_stop\ndata: {broken}\n\n"} {
		owner := cooperative.New()
		body := &capabilityBody{owner: owner, chunks: [][]byte{[]byte(wire)}}
		calls := 0
		diagnostics := []string{}
		aborts := 0
		stream, _ := sdkstream.New(owner, body, protocol.NewSDKSSEDecoder(nil), func(frame protocol.SDKSSEEvent) (*sdkstream.Value, error) {
			calls++
			return provider.ParseSDKFrame(frame, nil)
		}, func(*cooperative.Scope) { aborts++ }, func(name string, _ any) { diagnostics = append(diagnostics, name) })
		iterator, _ := stream.Iterator()
		err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			future, err := iterator.Next(scope)
			if err != nil {
				return err
			}
			result, failure := future.Await(scope)
			if wire[0:len("event: ping")] == "event: ping" {
				if failure != nil || result.Done {
					t.Fatal(result, failure)
				}
			} else {
				var sdk *provider.SDKStreamError
				if !errors.As(failure, &sdk) || sdk.Name != "SyntaxError" || aborts != 1 || body.returns != 1 || len(diagnostics) != 2 {
					t.Fatal(failure, aborts, body.returns, diagnostics)
				}
			}
			return nil
		})
		owner.Close()
		if err != nil || calls != 1 {
			t.Fatal(err, calls)
		}
	}
}
