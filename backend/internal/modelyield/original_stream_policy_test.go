package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestSDKProducerClockOriginalPolicyIdentityAndDeferredError(t *testing.T) {
	raw, e := os.ReadFile("testdata/producer-originals.json")
	if e != nil {
		t.Fatal(e)
	}
	var originals []struct {
		Requests []struct{ Prefix, Middle, Ending, Program string }
	}
	if e = json.Unmarshal(raw, &originals); e != nil {
		t.Fatal(e)
	}
	wire := ""
	for _, original := range originals {
		for _, request := range original.Requests {
			if request.Program == "api-error" {
				wire = request.Prefix + request.Middle + request.Ending
				break
			}
		}
	}
	if wire == "" {
		t.Fatal("missing original external terminal wire")
	}
	for _, input := range []struct {
		name string
		env  map[string]string
	}{
		{"absent", map[string]string{}},
		{"partial", map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1"}},
		{"aliases", map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "YES", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "2", "CLAUDE_ENABLE_STREAM_WATCHDOG": "true"}},
		{"invalid", map[string]string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "invalid", "CLAUDE_ENABLE_STREAM_WATCHDOG": "off"}},
	} {
		t.Run(input.name, func(t *testing.T) {
			policy := PreserveStreamErrorPolicyOriginalEnvironment(input.env)
			view := policy.OriginalEnvironment()
			if !reflect.DeepEqual(view, input.env) {
				t.Fatalf("raw original presence/value changed: %v != %v", view, input.env)
			}
			view["CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"] = "observer write"
			if !reflect.DeepEqual(policy.OriginalEnvironment(), input.env) {
				t.Fatal("observer mutated original producer snapshot")
			}
			posts, deltas := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				w.Header().Set("content-type", "text/event-stream")
				fmt.Fprint(w, wire)
			}))
			defer server.Close()
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			ids := 0
			e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				_, e := Run(context.Background(), queuedInput(server.URL), Dependencies{
					Session: session, AgentContext: binding, StreamPolicy: policy,
					StreamClock: &ProducerClock{NowMillis: func() float64 { return 1700000000000 }},
					Request:     requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { ids++; return fmt.Sprintf("original-policy-id-%d", ids) }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("unexpected original policy retry") }},
					Emit: func(value Yield) (requestretry.NoticeAction, error) {
						if value.Update != nil && len(value.Update.Event) != 0 {
							var event map[string]any
							_ = json.Unmarshal(value.Update.Event, &event)
							if event["type"] == "content_block_delta" {
								deltas++
							}
						}
						return requestretry.Resume, nil
					},
				})
				return e
			})
			var capability *CapabilityError
			if !errors.As(e, &capability) || capability.Boundary != "partial-stream error, retry and commit buffer" || posts != 1 || deltas != 1 {
				t.Fatalf("must defer unknown policy until actual error after delta: posts=%d deltas=%d err=%v", posts, deltas, e)
			}
		})
	}
}
