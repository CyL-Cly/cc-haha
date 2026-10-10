package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func queryWire(kind string) string {
	stop := "end_turn"
	if strings.Contains(kind, "limit") {
		stop = "max_tokens"
	}
	return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"original\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" + "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" + "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"original text\"}}\n\n" + "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" + fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q,\"stop_sequence\":null},\"usage\":{\"output_tokens\":2}}\n\n", stop) + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
}
func queuedInput(origin string) requestretry.Input {
	input, _ := contextFromFixture(fixtureInput{Env: map[string]string{"ANTHROPIC_BASE_URL": origin, "ANTHROPIC_API_KEY": "original-key", "CLAUDE_CODE_MAX_RETRIES": "0"}, HistorySources: []string{`{"type":"user","uuid":"original-user","message":{"role":"user","content":"original"}}`}, Model: "fixture-model", MaxOutputTokens: 32, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
	fast := false
	input.ResolvedFastMode = &fast
	return input
}
func TestQueuedProductionDemandAndFinally(t *testing.T) {
	for _, kind := range []string{"normal", "return-start", "queued-return", "caller", "cleanup", "return-assistant-limit", "full-limit", "consumer-error"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("content-type", "text/event-stream")
				w.Header().Set("request-id", "original-request")
				wire := queryWire(kind)
				prefix := strings.Index(wire, "event: content_block_start")
				_, _ = fmt.Fprint(w, wire[:prefix])
				w.(http.Flusher).Flush()
				if kind == "caller" || kind == "cleanup" {
					<-r.Context().Done()
					return
				}
				_, _ = fmt.Fprint(w, wire[prefix:])
			}))
			defer server.Close()
			owner := cooperative.New()
			defer owner.Close()
			input := queuedInput(server.URL)
			session, e := modelaccounting.NewSession(owner, input.Context.Options.Model, input.Context.Host.Env)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			ids := 0
			allocAtReturn := 0
			eventKinds := []string{}
			envelopes := []*streamhistoryEnvelopeForTest{}
			originalError := errors.New("original consumer rejection")
			var final protocol.JSONObject
			var outcome Outcome
			e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				var completion *ownerasync.OwnerFuture[struct{}]
				var it *QueryIterator
				var controls *ResourceControl
				var consume func(*cooperative.Scope, *ownerasync.OwnerFuture[QueryResult]) error
				settleReturn := func(next *cooperative.Scope) error {
					allocAtReturn = ids
					returned, e := it.Return(next, QueryValue{ReturnValue: json.RawMessage(`"original-return"`)})
					if e != nil {
						return e
					}
					_, e = returned.Then(next, func(fresh *cooperative.Scope, r QueryResult, failure error) error {
						if failure != nil {
							return completion.Reject(fresh, failure)
						}
						if !r.Done || string(r.Value.ReturnValue) != `"original-return"` {
							return completion.Reject(fresh, errors.New("original queued return value changed"))
						}
						return completion.Resolve(fresh, struct{}{})
					})
					return e
				}
				consume = func(next *cooperative.Scope, future *ownerasync.OwnerFuture[QueryResult]) error {
					_, e := future.Then(next, func(fresh *cooperative.Scope, r QueryResult, failure error) error {
						if failure != nil {
							return completion.Reject(fresh, failure)
						}
						if r.Done {
							return completion.Resolve(fresh, struct{}{})
						}
						if r.Value.Yield == nil {
							return completion.Reject(fresh, errors.New("missing live yield"))
						}
						update := r.Value.Yield.Update
						if update == nil {
							return completion.Reject(fresh, errors.New("unexpected retry notice"))
						}
						if update.Envelope != nil {
							envelopes = append(envelopes, &streamhistoryEnvelopeForTest{APIError: update.Envelope.APIError, UUID: update.Envelope.UUID})
							if kind == "return-assistant-limit" {
								return settleReturn(fresh)
							}
						} else {
							var event protocol.JSONObject
							_ = event.UnmarshalJSON(update.Event)
							eventKinds = append(eventKinds, event.String("type"))
						}
						if len(eventKinds) == 1 && len(envelopes) == 0 {
							if kind == "return-start" {
								return settleReturn(fresh)
							}
							if kind == "consumer-error" {
								return completion.Reject(fresh, originalError)
							}
							pending, e := it.Next(fresh)
							if e != nil {
								return completion.Reject(fresh, e)
							}
							if kind == "queued-return" {
								allocAtReturn = ids
								returned, e := it.Return(fresh, QueryValue{ReturnValue: json.RawMessage(`"original-return"`)})
								if e != nil {
									return completion.Reject(fresh, e)
								}
								_, e = pending.Then(fresh, func(n *cooperative.Scope, r QueryResult, failure error) error {
									if failure != nil {
										return completion.Reject(n, failure)
									}
									var event protocol.JSONObject
									_ = event.UnmarshalJSON(r.Value.Yield.Update.Event)
									if event.String("type") != "content_block_start" {
										return completion.Reject(n, errors.New("queued return overtook original pending Next"))
									}
									eventKinds = append(eventKinds, "content_block_start")
									_, e := returned.Then(n, func(n *cooperative.Scope, r QueryResult, failure error) error {
										if failure != nil {
											return completion.Reject(n, failure)
										}
										if !r.Done || string(r.Value.ReturnValue) != `"original-return"` {
											return completion.Reject(n, errors.New("queued return lost value"))
										}
										return completion.Resolve(n, struct{}{})
									})
									return e
								})
								return e
							}
							if kind == "caller" {
								cancel(errors.New("interrupt"))
							}
							if kind == "cleanup" {
								if e := controls.Cleanup(fresh); e != nil {
									return completion.Reject(fresh, e)
								}
							}
							return consume(fresh, pending)
						}
						pending, e := it.Next(fresh)
						if e != nil {
							return completion.Reject(fresh, e)
						}
						return consume(fresh, pending)
					})
					return e
				}
				deps := QueuedDependencies{AgentContext: mustMainAccountingContext(owner), Session: session, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { ids++; return fmt.Sprintf("id-%d", ids) }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("unexpected retry") }}}
				deps.Start = func(next *cooperative.Scope, iterator *QueryIterator, control *ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
					it = iterator
					controls = control
					completion, _ = ownerasync.NewOwnerFuture[struct{}](owner)
					future, e := it.Next(next)
					if e != nil {
						return nil, e
					}
					return completion, consume(next, future)
				}
				var failure error
				outcome, failure = RunQueued(ctx, input, deps)
				final, _ = session.Snapshot(scope)
				return failure
			})
			if kind == "consumer-error" {
				if e != originalError {
					t.Fatalf("consumer reason changed: %v", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if requests.Load() != 1 {
				t.Fatal("second model request", requests.Load())
			}
			var cost float64
			_ = json.Unmarshal(final.Get("totalCostUSD"), &cost)
			if kind == "normal" || strings.Contains(kind, "limit") {
				if cost != float64(float64(1.0/1e6)*3+float64(2.0/1e6)*15) {
					t.Fatal("finally usage not charged", cost)
				}
			} else if cost != 0 {
				t.Fatal("start-only stream charged", cost)
			}
			if kind == "normal" || kind == "full-limit" {
				if final.String("lastMainRequestID") != "original-request" {
					t.Fatal("normal fallthrough did not set request ID")
				}
			} else if final.Get("lastMainRequestID") != nil {
				t.Fatal("early termination ran code after finally")
			}
			if kind == "return-start" || kind == "queued-return" || kind == "return-assistant-limit" {
				if ids != allocAtReturn || outcome.Reason != "consumer_stopped" {
					t.Fatal("Feed advanced allocations after real return", ids, allocAtReturn, outcome)
				}
			}
			if kind == "caller" && len(envelopes) != 0 {
				t.Fatal("caller interrupt generated API error")
			}
			if kind == "cleanup" && (ctx.Err() != nil || len(envelopes) != 1 || !envelopes[0].APIError) {
				t.Fatal("SDK cleanup did not remain independent", ctx.Err(), envelopes)
			}
			if kind == "return-assistant-limit" && len(envelopes) != 1 {
				t.Fatal("Feed emitted later limit feedback before demand", envelopes)
			}
		})
	}
}

type streamhistoryEnvelopeForTest struct {
	APIError bool
	UUID     string
}

func TestQueuedAdmissionAndResourceOwnershipBeforeRequest(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	input := queuedInput("http://127.0.0.1:1")
	session, _ := modelaccounting.NewSession(owner, input.Context.Options.Model, input.Context.Host.Env)
	foreignSession, _ := modelaccounting.NewSession(foreign, input.Context.Options.Model, input.Context.Host.Env)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		requests := 0
		deps := QueuedDependencies{AgentContext: mustMainAccountingContext(owner), Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, ObserveRequest: func(string, http.Header, json.RawMessage) { requests++ }}, Session: session, Start: func(*cooperative.Scope, *QueryIterator, *ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
			f, _ := ownerasync.NewOwnerFuture[struct{}](owner)
			return f, nil
		}}
		for _, mode := range []string{"nil-context", "foreign-owner", "nil-session", "foreign-session", "nil-start", "no-first-next", "start-error", "foreign-completion", "nil-agent-binding", "foreign-agent-binding"} {
			current := deps
			ctx := context.Background()
			failure := errors.New("original start rejection")
			switch mode {
			case "nil-context":
				ctx = nil
			case "foreign-owner":
				current.Request.StreamOwner = foreign
			case "nil-agent-binding":
				current.AgentContext = nil
			case "foreign-agent-binding":
				current.AgentContext = mustMainAccountingContext(foreign)
			case "nil-session":
				current.Session = nil
			case "foreign-session":
				current.Session = foreignSession
			case "nil-start":
				current.Start = nil
			case "start-error":
				current.Start = func(*cooperative.Scope, *QueryIterator, *ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
					return nil, failure
				}
			case "foreign-completion":
				current.Start = func(next *cooperative.Scope, it *QueryIterator, _ *ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
					if _, e := it.Next(next); e != nil {
						return nil, e
					}
					f, _ := ownerasync.NewOwnerFuture[struct{}](foreign)
					return f, nil
				}
			}
			if _, e := RunQueued(ctx, input, current); e == nil || mode == "start-error" && e != failure {
				return fmt.Errorf("invalid queued admission %s: %v", mode, e)
			}
		}
		control := &ResourceControl{owner: owner}
		if e := control.Cleanup(scope); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("cleanup without SDK lease accepted")
		}
		if present, _, e := control.State(scope); e != nil || present {
			return errors.New("unopened SDK state projected")
		}
		if _, _, e := (&ResourceControl{owner: foreign}).State(scope); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("foreign controller state read")
		}
		if e := (*ResourceControl)(nil).Cleanup(scope); !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("nil controller cleanup")
		}
		if requests != 0 {
			return errors.New("queued admission prepared HTTP request")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
