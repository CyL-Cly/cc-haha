package modelyield

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Original Source1954 is outside creation's API-error catch. These actual
// loopback inputs exercise the current producer without an injected API result.
func TestPermissionPrequerySyncErrorStopsPhysicalHTTP(t *testing.T) {
	var physical atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		physical.Add(1)
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, queryWire("normal"))
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	session, _ := modelaccounting.NewOwnedSession(owner, nil)
	binding, _ := modelaccounting.NewMainContext(owner)
	cause := errors.New("original prequery permission throw")
	gets := 0
	emits := 0
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := Run(context.Background(), queuedInput(server.URL), Dependencies{Session: session, AgentContext: binding, SuccessDuration: &modelaccounting.SuccessDuration{NowMillis: func() float64 { return 1700000000000 }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
			gets++
			return nil, cause
		}}, Request: requestretry.Dependencies{StreamOwner: owner, StreamScope: scope, UUID: func() string { return "original-id" }, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("unexpected retry") }}, Emit: func(Yield) (requestretry.NoticeAction, error) { emits++; return requestretry.Resume, nil }})
		return e
	})
	if err != cause {
		t.Errorf("lost original getter error identity: %v", err)
	}
	if physical.Load() != 0 || emits != 0 || gets != 1 {
		t.Errorf("prequery throw reached request/creation: requests=%d emits=%d gets=%d", physical.Load(), emits, gets)
	}
}
func TestPermissionEmptyObjectSourceModeUndefined(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	s, _ := modelaccounting.NewOwnedSession(owner, nil)
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		clock := float64(100)
		p, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
		_ = p.Resolve(scope, protocol.NewJSONObject())
		a, e := s.BeginDuration(scope, modelaccounting.SuccessDuration{NowMillis: func() float64 { return clock }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) { return p, nil }})
		if e != nil {
			return e
		}
		clock = 400
		if e = a.RegisterSuccess(scope); e != nil {
			return e
		}
		if _, e = p.Await(scope); e != nil {
			return e
		}
		v, e := s.DurationSnapshot(scope)
		if e != nil {
			return e
		}
		if string(v.Get("totalAPIDuration")) != "300" {
			t.Errorf("Source {} modeundefined incorrectly rejected: %s", v.Get("totalAPIDuration"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
