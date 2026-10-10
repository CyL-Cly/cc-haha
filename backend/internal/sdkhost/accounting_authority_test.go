package sdkhost_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

// The original request inputs and physical wires are from the same original
// boundary. A first ASK can stop only after its actual message_delta output;
// the next ASK's actual result must include both producer finally charges.
func TestActualSDKProducerFinallyCostSurvivesDeltaStop(t *testing.T) {
	for _, kind := range []string{"normal", "return", "caller", "second-host-rejected", "shared-producer"} {
		t.Run(kind, func(t *testing.T) {
			var original requestretry.Input
			first := true
			stopped := false
			modelCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stdout, result := originalBoundary(t, nativeControls{
				Configure: func(o *sdkhost.Options) {
					observe := o.ObserveModelInput
					o.ObserveModelInput = func(v requestretry.Input) {
						original = v
						if observe != nil {
							observe(v)
						}
					}
					enqueue := o.Output.Enqueue
					o.Output.Enqueue = func(b []byte) error {
						e := object(b)
						if first && e.String("type") == "stream_event" && object(e.Get("event")).String("type") == "message_delta" {
							stopped = true
							if kind == "caller" {
								cancel()
							}
						}
						return enqueue(b)
					}
					o.Output.Receipt = func(b []byte) *cooperative.Yield {
						e := object(b)
						action := cooperative.Resume
						if first && kind == "return" && e.String("type") == "stream_event" && object(e.Get("event")).String("type") == "message_delta" {
							action = cooperative.Stop
						}
						receipt := cooperative.NewYield()
						receipt.Resolve(action, nil)
						return receipt
					}
				},
				Run: func(o sdkhost.Options, h *sdkhost.Host, inputs []protocol.JSONObject) error {
					if e := o.Owner.Run(context.Background(), func(s *cooperative.Scope) error { return h.RunOne(s, modelCtx, inputs[0]) }); e != nil {
						return e
					}
					first = false

					if kind == "second-host-rejected" {
						if _, e := sdkhost.New(o); e == nil {
							t.Fatal("second SDK Host borrower was admitted")
						}
					}
					if kind == "shared-producer" {
						if e := o.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
							ledger, e := o.Controller.ModelAccountingFor(o.Owner)
							if e != nil {
								return e
							}
							binding, e := modelaccounting.NewMainContext(o.Owner)
							if e != nil {
								return e
							}
							_, e = modelyield.Run(context.Background(), original, modelyield.Dependencies{
								Session: ledger, AgentContext: binding,
								Request: requestretry.Dependencies{StreamOwner: o.Owner, StreamScope: scope, UUID: o.UUID, Now: o.Now, Random: func() float64 { return 0 }, TLSState: o.TLSState, ObserveRequest: o.ObserveRequest, NativeOpenIO: providernative.NewFactory(o.Owner, scope, nil), Sleep: func(context.Context, float64) error { return cooperative.ErrRestricted }, BodyIOAwait: func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
									return scope.AwaitBytes(cooperative.StreamNext, work)
								}},
								Emit: func(modelyield.Yield) (requestretry.NoticeAction, error) { return requestretry.Resume, nil },
							})
							return e
						}); e != nil {
							return e
						}
					}
					return o.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return h.RunOne(scope, context.Background(), inputs[1]) })
				},
			})
			if !stopped || result["runError"] != nil {
				t.Fatalf("actual delta boundary not completed: stopped=%v error=%v", stopped, result["runError"])
			}
			wantRequests := 2
			if kind == "shared-producer" {
				wantRequests = 3
			}
			if len(result["requests"].([]string)) != wantRequests {
				t.Fatal("first return/cancel changed physical request cardinality")
			}
			var last protocol.JSONObject
			for _, line := range splitAccountingLines(stdout) {
				var value protocol.JSONObject
				if e := value.UnmarshalJSON(line); e != nil {
					t.Fatal(e)
				}
				if value.String("type") == "result" {
					last = value
				}
			}
			if last.String("type") != "result" {
				t.Fatal("second actual ASK result absent")
			}
			var got float64
			if e := json.Unmarshal(last.Get("total_cost_usd"), &got); e != nil {
				t.Fatal(e)
			}
			// Original first wire: input5/output9; original second: input13/output7.
			// Finite relay pricing is $3/$15 per million, independent of Source outputs.
			want := (5*3+9*15)/1e6 + (13*3+7*15)/1e6
			wantInput, wantOutput := "18", "16"
			if kind == "shared-producer" {
				want += (13*3 + 7*15) / 1e6
				wantInput, wantOutput = "31", "23"
			}
			if math.Abs(got-want) > 1e-15 {
				t.Fatalf("actual producer finally cumulative cost: got %.17g want %.17g", got, want)
			}
			models := object(last.Get("modelUsage"))
			usage := object(models.Get("fixture-model"))
			if string(usage.Get("inputTokens")) != wantInput || string(usage.Get("outputTokens")) != wantOutput {
				t.Fatalf("actual cumulative model usage lost or doubled: %s", last.Get("modelUsage"))
			}
		})
	}
}
func splitAccountingLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				lines = append(lines, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, b[start:])
	}
	return lines
}
