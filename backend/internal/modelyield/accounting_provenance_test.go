package modelyield

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func drainAccountingOriginal(next *cooperative.Scope, owner *cooperative.Owner, iterator *QueryIterator) (*ownerasync.OwnerFuture[struct{}], error) {
	done, err := ownerasync.NewOwnerFuture[struct{}](owner)
	if err != nil {
		return nil, err
	}
	var consume func(*cooperative.Scope) error
	consume = func(scope *cooperative.Scope) error {
		pending, failure := iterator.Next(scope)
		if failure != nil {
			return done.Reject(scope, failure)
		}
		_, failure = pending.Then(scope, func(fresh *cooperative.Scope, result QueryResult, failure error) error {
			if failure != nil {
				return done.Reject(fresh, failure)
			}
			if result.Done {
				return done.Resolve(fresh, struct{}{})
			}
			return consume(fresh)
		})
		return failure
	}
	return done, consume(next)
}

// Deterministic API construction risk, not a claimed new actual Source red:
// 737 accepts a separately seeded ledger while the real request uses model B,
// and writes lastID for a non-main query source. Both must follow original input.
func Test737UnboundAccountingRequestRisks(t *testing.T) {
	for _, kind := range []string{"off-model", "non-main-source"} {
		t.Run(kind, func(t *testing.T) {
			var actualModel atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request protocol.JSONObject
				if err := request.UnmarshalJSON(readRequestBytes(r)); err != nil {
					return
				}
				actualModel.Store(request.String("model"))
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Request-Id", "original-current-request")
				_, _ = fmt.Fprint(w, queryWire("normal"))
			}))
			defer server.Close()
			owner := cooperative.New()
			defer owner.Close()
			input := queuedInput(server.URL)
			if kind == "off-model" {
				input.Context.Options.Model = "deepseek-flash"
			}
			if kind == "non-main-source" {
				input.Context.Options.QuerySource = "auxiliary-original-query"
			}
			session, err := modelaccounting.NewSession(owner, "fixture-model", input.Context.Host.Env)
			if err != nil {
				t.Fatal(err)
			}
			var ledger protocol.JSONObject
			err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				deps := QueuedDependencies{AgentContext: mustMainAccountingContext(owner), Session: session, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "original-native-test-id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("unexpected retry") }}}
				deps.Start = func(next *cooperative.Scope, it *QueryIterator, _ *ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
					return drainAccountingOriginal(next, owner, it)
				}
				_, failure := RunQueued(context.Background(), input, deps)
				if failure != nil {
					return failure
				}
				ledger, failure = session.Snapshot(scope)
				return failure
			})
			if err != nil {
				t.Fatal(err)
			}
			if actualModel.Load() != input.Context.Options.Model {
				t.Fatal("independent original request model changed", actualModel.Load())
			}
			if kind == "off-model" {
				var models protocol.JSONObject
				_ = models.UnmarshalJSON(ledger.Get("modelUsage"))
				if models.Get("deepseek-flash") == nil || models.Get("fixture-model") != nil {
					t.Fatalf("unbound session model accepted real request: %s", ledger.Get("modelUsage"))
				}
			} else if ledger.Get("lastMainRequestID") != nil {
				t.Fatalf("non-main original source wrote last ID: %s", ledger.Get("lastMainRequestID"))
			}
		})
	}
}
func readRequestBytes(r *http.Request) []byte {
	out, _ := io.ReadAll(r.Body)
	return out
}

func mustMainAccountingContext(owner *cooperative.Owner) *modelaccounting.AgentContextBinding {
	binding, _ := modelaccounting.NewMainContext(owner)
	return binding
}
