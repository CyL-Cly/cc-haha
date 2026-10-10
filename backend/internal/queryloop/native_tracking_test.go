package queryloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestNativeActualRoundTrackingReachesPreparedParamsWithoutExtraUUID(t *testing.T) {
	for _, initial := range []*Tracking{nil, {ChainID: "inherited-chain", Depth: 7}} {
		t.Run("original tracking", func(t *testing.T) {
			posts := 0
			h := testHarness(t, func(w http.ResponseWriter, r *http.Request) {
				posts++
				w.Header().Set("Content-Type", "text/event-stream")
				wire := completeText()
				if posts == 1 {
					wire = completeTool()
				}
				_, _ = io.WriteString(w, wire)
			})
			h.Options.InitialTracking = initial
			resolve := h.Host.ResolveModelInput
			h.Host.ResolveModelInput = func(ctx context.Context, round Round) (requestretry.Input, error) {
				in, err := resolve(ctx, round)
				in.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
				// A stale host value cannot replace this actual query iteration.
				in.Context.Options.QueryTracking = json.RawMessage(`{"chainId":"stale","depth":99}`)
				return in, err
			}
			got := []Tracking{}
			h.Host.Preflight = func(_ context.Context, round Round) ([]Message, error) {
				got = append(got, round.Tracking)
				return round.History, nil
			}
			params := 0
			h.Host.Request.OnParamsPrepared = func(result *requestcontext.Result) error {
				params++
				if result.Params == nil {
					t.Error("no actual Native params")
				}
				return nil
			}
			want := []Tracking{{ChainID: "1", Depth: 0}, {ChainID: "1", Depth: 1}}
			if initial != nil {
				want = []Tracking{{ChainID: initial.ChainID, Depth: 8}, {ChainID: initial.ChainID, Depth: 9}}
			}
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			var out Outcome
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				h.Host.Request.StreamOwner, h.Host.Request.StreamScope = owner, scope
				h.Host.Session, h.Host.AgentContext = session, binding
				emptySettings := func() (*protocol.JSONObject, error) { empty := protocol.NewJSONObject(); return &empty, nil }
				if err := session.InstallNativeCalculator(scope, func(model string, env map[string]string) (*modelaccounting.Calculator, error) {
					return modelaccounting.NewNativeCalculator(model, modelaccounting.NativeCalculatorOptions{Environment: env, Settings: emptySettings, Global: emptySettings})
				}); err != nil {
					return err
				}
				var lastPermission *ownerasync.OwnerFuture[protocol.JSONObject]
				h.Host.SuccessDuration = &modelaccounting.SuccessDuration{NowMillis: func() float64 { return 100 }, GetPermission: func(next *cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
					future, err := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
					if err == nil {
						err = future.Resolve(next, protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: json.RawMessage(`"default"`)}))
					}
					lastPermission = future
					return future, err
				}}
				var err error
				out, err = h.run(context.Background())
				if err != nil {
					return err
				}
				if _, err := lastPermission.Await(scope); err != nil {
					return err
				}
				logs, err := session.QueryLogSnapshot(scope)
				if err != nil || len(logs) != 2 {
					return fmt.Errorf("actual query tracking log lifecycle: %v len%d", err, len(logs))
				}
				for index, event := range logs {
					var metadata protocol.JSONObject
					_ = metadata.UnmarshalJSON(event.Get("metadata"))
					if metadata.String("queryChainId") != want[index].ChainID || string(metadata.Get("queryDepth")) != fmt.Sprint(want[index].Depth) {
						return fmt.Errorf("actual Native round tracking did not reach params logging: %s", event.Get("metadata"))
					}
				}
				return nil
			})
			if err != nil || out.Reason != "completed" || posts != 2 || params != 2 || !reflect.DeepEqual(got, want) {
				t.Fatalf("actual Native query tracking: %+v %v posts%d params%d got%+v", out, err, posts, params, got)
			}
			// One chain ID only for a new chain; the existing assistant envelope
			// producer allocates one ID per response in both profiles.
			wantIDs := 3
			if initial != nil {
				wantIDs = 2
			}
			if h.UUIDs != wantIDs {
				t.Fatalf("query logging allocated another chain UUID: %d want %d", h.UUIDs, wantIDs)
			}
		})
	}
}
