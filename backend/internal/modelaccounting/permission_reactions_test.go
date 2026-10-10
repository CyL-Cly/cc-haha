package modelaccounting

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerlogging"
	"testing"
)

func TestActualPermissionBothThenDistinctFromHolderAndNonemptyQueryQueue(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	session, _ := NewOwnedSession(owner, nil)
	tracker, _ := ownerasync.NewIgnoredRejections(owner)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		clock := float64(100)
		holder, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
		gets := 0
		a, e := session.BeginDuration(scope, SuccessDuration{NowMillis: func() float64 { return clock }, Ignored: tracker, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
			gets++
			return holder, nil
		}})
		if e != nil {
			return e
		}
		if e = a.RegisterQuery(scope, providerlogging.QueryInput{Model: "fixture-model", MessagesLength: 1, Temperature: 1, QuerySource: "sdk", ThinkingType: "disabled"}); e != nil {
			return e
		}
		if e = a.RegisterQuery(scope, providerlogging.QueryInput{}); e == nil {
			t.Fatal("prequery repeated")
		}
		clock = 400
		if e = a.RegisterSuccess(scope); e != nil {
			return e
		}
		logs, _ := session.QueryLogSnapshot(scope)
		before, _ := session.DurationSnapshot(scope)
		if gets != 2 || len(logs) != 0 || string(before.Get("totalAPIDuration")) != "0" {
			t.Fatal("held logging was awaited or empty callback substituted")
		}
		_ = holder.Resolve(scope, protocol.NewJSONObject())
		_, _ = holder.Await(scope)
		logs, _ = session.QueryLogSnapshot(scope)
		after, _ := session.DurationSnapshot(scope)
		pending, _ := tracker.Pending(scope)
		if len(logs) != 1 || string(after.Get("totalAPIDuration")) != "300" || len(pending) != 0 {
			t.Fatalf("Source empty context/queue differs: %v %s", len(logs), after.Get("totalAPIDuration"))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestActualPermissionRejectedNullSyncAndForeignOwnership(t *testing.T) {
	for _, kind := range []string{"rejected", "null", "nil", "throw", "foreign-queue", "foreign-tracker"} {
		t.Run(kind, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			other := cooperative.New()
			defer other.Close()
			session, _ := NewOwnedSession(owner, nil)
			tracker, _ := ownerasync.NewIgnoredRejections(owner)
			cause := errors.New("original getter rejection")
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				p, _ := ownerasync.NewOwnerFuture[protocol.JSONObject](owner)
				if kind == "rejected" {
					_ = p.Reject(scope, cause)
				} else {
					_ = p.Resolve(scope, protocol.JSONObject{})
				}
				original := SuccessDuration{NowMillis: func() float64 { return 1 }, Ignored: tracker, GetPermission: func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
					if kind == "nil" {
						return nil, nil
					}
					if kind == "throw" {
						return nil, cause
					}
					return p, nil
				}}
				if kind == "foreign-queue" {
					original.QueryLog, _ = providerlogging.New(other)
				}
				if kind == "foreign-tracker" {
					original.Ignored, _ = ownerasync.NewIgnoredRejections(other)
				}
				a, e := session.BeginDuration(scope, original)
				if e != nil {
					return e
				}
				e = a.RegisterQuery(scope, providerlogging.QueryInput{})
				if kind == "nil" || kind == "throw" || kind == "foreign-queue" || kind == "foreign-tracker" {
					if e == nil {
						t.Fatal("missing synchronous admission failure")
					}
					if kind == "throw" && e != cause {
						t.Fatal("original sync identity")
					}
					return nil
				}
				if e != nil {
					return e
				}
				if e = a.RegisterSuccess(scope); e != nil {
					return e
				}
				_, _ = p.Await(scope)
				pending, _ := tracker.Pending(scope)
				if len(pending) != 2 {
					t.Fatalf("want two actual ignored dependents, got %d", len(pending))
				}
				if pending[0].Promise == pending[1].Promise || pending[0].Holder != p.Identity() {
					t.Fatal("dependent identity")
				}
				if kind == "rejected" {
					if pending[0].Original != cause || pending[1].Original != cause {
						t.Fatal("original reason lost")
					}
				} else {
					if _, ok := pending[0].Original.(*PermissionTypeError); !ok {
						t.Fatal("null-access wrong failure owner")
					}
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
