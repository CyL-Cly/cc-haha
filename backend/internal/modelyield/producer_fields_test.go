package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestProducerOriginalErrorThrowAndDecode(t *testing.T) {
	raw, err := os.ReadFile("testdata/producer-originals.json")
	if err != nil {
		t.Fatal(err)
	}
	var originals []struct {
		ID       string `json:"id"`
		Requests []struct {
			Prefix, Middle, Ending, Program, ThrowMessage string
			ClockAtMessageDelta                           float64 `json:"clockAtMessageDelta"`
		} `json:"requests"`
	}
	if err = json.Unmarshal(raw, &originals); err != nil {
		t.Fatal(err)
	}
	for _, p := range originals[:6] {
		spec := p.Requests[0]
		if spec.Program == "return" || spec.Program == "caller-abort" {
			continue
		}
		t.Run(p.ID, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				w.Header().Set("request-id", "original-duration-response")
				fmt.Fprint(w, spec.Prefix+spec.Middle+spec.Ending)
			}))
			defer server.Close()
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			clock := float64(1700000000000)
			input := queuedInput(server.URL)
			ids := 0
			var yielded []string
			var decode int64
			var outcome Outcome
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				permission, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
				_ = permission.Resolve(scope, protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: json.RawMessage(`"default"`)}))
				completion, _ := ownerasync.NewOwnerFuture[struct{}](owner)
				var iterator *QueryIterator
				thrown := false
				var consume func(*cooperative.Scope, *ownerasync.OwnerFuture[QueryResult]) error
				consume = func(s *cooperative.Scope, f *ownerasync.OwnerFuture[QueryResult]) error {
					_, e := f.Then(s, func(next *cooperative.Scope, r QueryResult, failure error) error {
						if failure != nil {
							return completion.Reject(next, failure)
						}
						if r.Done {
							return completion.Resolve(next, struct{}{})
						}
						if r.Value.Yield == nil || r.Value.Yield.Update == nil {
							return completion.Reject(next, errors.New("missing producer update"))
						}
						update := r.Value.Yield.Update
						if update.Envelope != nil {
							b, _ := update.Envelope.MarshalJSON()
							var env protocol.JSONObject
							_ = env.UnmarshalJSON(b)
							msg := env.Get("message")
							var m protocol.JSONObject
							_ = m.UnmarshalJSON(msg)
							var content []protocol.JSONObject
							_ = json.Unmarshal(m.Get("content"), &content)
							kind := "assistant"
							if update.Envelope.APIError {
								kind = "API Error: " + strings.TrimPrefix(content[0].String("text"), "API Error: ")
							}
							yielded = append(yielded, kind)
						} else {
							var event protocol.JSONObject
							_ = event.UnmarshalJSON(update.Event)
							kind := event.String("type")
							yielded = append(yielded, kind)
							if kind == "message_stop" {
								decode = update.Timing.DecodeMillis
							}
							if kind == "message_delta" {
								clock = spec.ClockAtMessageDelta
								if spec.Program == "throw" && !thrown {
									thrown = true
									f, e := iterator.Throw(next, originalThrow(spec.ThrowMessage))
									if e != nil {
										return completion.Reject(next, e)
									}
									return consume(next, f)
								}
							}
						}
						f, e := iterator.Next(next)
						if e != nil {
							return completion.Reject(next, e)
						}
						return consume(next, f)
					})
					return e
				}
				deps := QueuedDependencies{Session: session, StreamClock: &ProducerClock{NowMillis: func() float64 { return clock }}, StreamPolicy: originalPolicyForTest(), AgentContext: binding, SuccessDuration: &modelaccounting.SuccessDuration{NowMillis: func() float64 { return clock }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) { return permission, nil }}, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { ids++; return fmt.Sprintf("original-id-%d", ids) }, Now: func() time.Time { return time.UnixMilli(int64(clock)) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("unexpected retry") }}}
				deps.Start = func(s *cooperative.Scope, it *QueryIterator, _ *ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
					iterator = it
					f, e := it.Next(s)
					if e != nil {
						return nil, e
					}
					return completion, consume(s, f)
				}
				var e error
				outcome, e = RunQueued(context.Background(), input, deps)
				return e
			})
			if err != nil {
				t.Errorf("actual producer rejected instead of ordered catch yields: %v", err)
			}
			want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta"}
			if spec.Program == "throw" {
				want = append(want, "assistant", "API Error: "+spec.ThrowMessage)
			} else if spec.Program == "api-error" {
				want = append(want, "assistant", "API Error: original external terminal SSE error")
			} else {
				want = append(want, "message_stop", "assistant")
				if decode != 123 {
					t.Errorf("decodeMs=%d want actual clock elapsed123", decode)
				}
			}
			if fmt.Sprint(yielded) != fmt.Sprint(want) {
				t.Errorf("ordered yields got %v want %v", yielded, want)
			}
			if outcome.Reason != "returned" {
				t.Errorf("producer completion=%+v", outcome)
			}
		})
	}
}
