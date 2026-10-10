package modelaccounting

import (
	"context"
	"errors"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func durationPermission() protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quote("default")})
}
func TestSessionDurationOriginalPromiseAndLateClock(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	session, _ := NewOwnedSession(owner, nil)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		originalCost, _ := session.Snapshot(scope)
		originalBytes, _ := originalCost.MarshalJSON()
		clock := []float64{100, 101, 200, 400, 405}
		calls := 0
		now := func() float64 { value := clock[calls]; calls++; return value }
		promise, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
		gets := 0
		attempt, e := session.BeginDuration(scope, SuccessDuration{NowMillis: now, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
			gets++
			return promise, nil
		}})
		if e != nil {
			return e
		}
		if e = attempt.StartAttempt(scope); e != nil {
			return e
		}
		if e = attempt.RegisterSuccess(scope); e != nil {
			return e
		}
		if e = attempt.RegisterSuccess(scope); e != nil {
			return e
		}
		if calls != 3 || gets != 1 {
			t.Errorf("success waited/repeated primitive reads: calls%d gets%d", calls, gets)
		}
		before, _ := session.DurationSnapshot(scope)
		if string(before.Get("totalAPIDuration")) != "0" {
			t.Error("held permission was drained")
		}
		if e = attempt.StartAttempt(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("completed producer reopened attempt")
		}
		if e = promise.Resolve(scope, durationPermission()); e != nil {
			return e
		}
		// Await the same original Promise, after observing pre-reaction state.
		if _, e = promise.Await(scope); e != nil {
			return e
		}
		after, _ := session.DurationSnapshot(scope)
		if string(after.Get("totalAPIDuration")) != "305" || string(after.Get("totalAPIDurationWithoutRetries")) != "200" {
			t.Errorf("late separate clocks/attempt start: %s", raw(after))
		}
		cost, _ := session.Snapshot(scope)
		costBytes, _ := cost.MarshalJSON()
		if string(costBytes) != string(originalBytes) {
			t.Error("duration changed existing cost snapshot contract")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestSessionDurationReactionsUseCurrentLedger(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	s, _ := NewOwnedSession(owner, nil)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		clock := float64(10)
		p, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
		a, e := s.BeginDuration(scope, SuccessDuration{NowMillis: func() float64 { return clock }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) { return p, nil }})
		if e != nil {
			return e
		}
		if e = a.StartAttempt(scope); e != nil {
			return e
		}
		if e = a.RegisterSuccess(scope); e != nil {
			return e
		}
		if e = s.addDuration(scope, 7, 9); e != nil {
			return e
		}
		clock = 30
		if e = p.Resolve(scope, durationPermission()); e != nil {
			return e
		}
		if _, e = p.Await(scope); e != nil {
			return e
		}
		v, _ := s.DurationSnapshot(scope)
		if string(v.Get("totalAPIDuration")) != "27" || string(v.Get("totalAPIDurationWithoutRetries")) != "29" {
			t.Errorf("late reaction overwrote current ledger: %s", raw(v))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestSessionDurationPermissionFailuresDoNotUndoCost(t *testing.T) {
	for _, kind := range []string{"rejected", "getter-throw", "nil", "foreign", "bad-context"} {
		t.Run(kind, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			other := cooperative.New()
			defer other.Close()
			s, _ := NewOwnedSession(owner, nil)
			cause := errors.New("original permission rejection")
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				p, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
				if kind == "rejected" {
					if e := p.Reject(scope, cause); e != nil {
						return e
					}
				} else if kind == "bad-context" {
					if e := p.Resolve(scope, protocol.NewJSONObject()); e != nil {
						return e
					}
				}
				if kind == "foreign" {
					p, _ = ownerasync.NewOwnerFuture[protocol.JSONObject](other)
				}
				a, e := s.BeginDuration(scope, SuccessDuration{NowMillis: func() float64 { return 10 }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
					if kind == "getter-throw" {
						return nil, cause
					}
					if kind == "nil" {
						return nil, nil
					}
					return p, nil
				}})
				if e != nil {
					return e
				}
				e = a.RegisterSuccess(scope)
				switch kind {
				case "getter-throw":
					if e != cause {
						t.Error("getter original error replaced")
					}
				case "nil":
					if e == nil {
						t.Error("nil Promise accepted")
					}
				case "foreign":
					if !errors.Is(e, cooperative.ErrRestricted) {
						t.Error("foreign Promise accepted")
					}
				case "rejected", "bad-context":
					if e != nil {
						return e
					}
					_, _ = p.Await(scope)
				}
				v, _ := s.DurationSnapshot(scope)
				if string(v.Get("totalAPIDuration")) != "0" {
					t.Error("failed permission charged duration")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestSessionDurationScopeGuards(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	s, _ := NewOwnedSession(owner, nil)
	var a *DurationAttempt
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := s.BeginDuration(scope, SuccessDuration{}); e == nil {
			t.Error("missing primitive accepted")
		}
		var e error
		a, e = s.BeginDuration(scope, SuccessDuration{NowMillis: func() float64 { return 1 }, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) { return nil, nil }})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := s.BeginDuration(scope, SuccessDuration{}); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("foreign Begin")
		}
		if _, e := s.DurationSnapshot(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("foreign snapshot")
		}
		if e := a.StartAttempt(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("foreign attempt")
		}
		if e := a.RegisterSuccess(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("foreign success")
		}
		if e := s.addDuration(scope, 1, 1); !errors.Is(e, cooperative.ErrRestricted) {
			t.Error("foreign add")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var nilSession *Session
	var nilAttempt *DurationAttempt
	if _, e := nilSession.DurationSnapshot(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error("nil snapshot")
	}
	if _, e := nilSession.BeginDuration(nil, SuccessDuration{}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error("nil Begin")
	}
	if e := nilAttempt.RegisterSuccess(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error("nil success")
	}
	if e := nilAttempt.StartAttempt(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Error("nil attempt")
	}
}
