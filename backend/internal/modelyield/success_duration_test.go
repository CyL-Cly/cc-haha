package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestSuccessDurationNormalOnlyAndOriginalFinally(t *testing.T) {
	for _, mode := range []string{"normal", "held", "return", "caller", "api-error", "throw", "getter-throw", "missing-clock", "missing-session"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Request-Id", "original-success-duration-ID")
				wire := queryWire("normal")
				if mode == "api-error" {
					wire = responseStart
				}
				fmt.Fprint(w, wire)
			}))
			defer server.Close()
			owner := cooperative.New()
			defer owner.Close()
			session, _ := modelaccounting.NewOwnedSession(owner, nil)
			binding, _ := modelaccounting.NewMainContext(owner)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clock := float64(100)
			gets := 0
			cause := errors.New("original continuation rejection")
			e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				p, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
				permission := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: json.RawMessage(`"default"`)})
				if e := p.Resolve(scope, permission); e != nil {
					return e
				}
				duration := modelaccounting.SuccessDuration{NowMillis: func() float64 { return clock }, GetPermission: func(s *cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
					gets++
					account, e := session.Snapshot(s)
					if e != nil {
						return nil, e
					}
					if gets > 1 && (string(account.Get("totalCostUSD")) == "0" || account.String("lastMainRequestID") != "original-success-duration-ID") {
						t.Error("permission getter preceded actual cost/lastID finally")
					}
					if mode == "getter-throw" && gets > 1 {
						return nil, cause
					}
					return p, nil
				}}
				deps := Dependencies{Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "original-duration-uuid" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("unexpected original retry") }}, Session: session, AgentContext: binding, SuccessDuration: &duration}
				if mode == "missing-clock" {
					duration.NowMillis = nil
				}
				if mode == "missing-session" {
					deps.Session = nil
					deps.AgentContext = nil
				}
				deps.Emit = func(y Yield) (requestretry.NoticeAction, error) {
					if y.Update != nil {
						var event protocol.JSONObject
						_ = event.UnmarshalJSON(y.Update.Event)
						if event.String("type") == "message_delta" {
							clock = 123
							switch mode {
							case "held":
								p, _ = ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
							case "return":
								return requestretry.Stop, nil
							case "caller":
								cancel()
							case "throw":
								return requestretry.Resume, cause
							}
						}
					}
					return requestretry.Resume, nil
				}
				_, failure := Run(ctx, queuedInput(server.URL), deps)
				switch mode {
				case "throw", "getter-throw":
					if failure != cause {
						t.Errorf("original failure priority lost: %v", failure)
					}
				case "missing-clock", "missing-session":
					if failure == nil {
						t.Error("unbound timing admitted")
					}
				default:
					if failure != nil {
						return failure
					}
				}
				immediate, _ := session.DurationSnapshot(scope)
				if string(immediate.Get("totalAPIDuration")) != "0" {
					t.Error("Run drained fire-and-forget logging")
				}
				if mode == "normal" || mode == "held" {
					if gets != 2 {
						t.Error("success permission getter missing")
					}
					if mode == "held" {
						clock = 987
						if e := p.Resolve(scope, permission); e != nil {
							return e
						}
					}
					if _, e := p.Await(scope); e != nil {
						return e
					}
					after, _ := session.DurationSnapshot(scope)
					want := "23"
					if mode == "held" {
						want = "887"
					}
					if string(after.Get("totalAPIDuration")) != want || string(after.Get("totalAPIDurationWithoutRetries")) != want {
						t.Errorf("late normal duration: %s want %s", after.Get("totalAPIDuration"), want)
					}
				} else if mode != "getter-throw" && mode != "missing-clock" && mode != "missing-session" && gets != 1 {
					t.Errorf("non-normal producer registered success: %s", mode)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
		})
	}
}
