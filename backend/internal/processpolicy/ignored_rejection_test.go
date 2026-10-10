package processpolicy

import (
	"bytes"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"testing"
)

func TestActualAdmittedPolicySameDependentAndOriginalReason(t *testing.T) {
	for _, kind := range []Kind{Native, CLIHelper, ServerHelper} {
		t.Run(string(kind), func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			var out bytes.Buffer
			runtime, _ := New(Options{Owner: owner, Kind: kind, WriteStderr: func(b []byte) error { _, e := out.Write(b); return e }})
			cause := errors.New("original rejected permission")
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				holder, _ := ownerasync.NewOwnerFuture[int](owner)
				child, _ := ownerasync.NewOwnerFuture[int](owner)
				notice := ownerasync.IgnoredRejection{Promise: child.Identity(), Holder: holder.Identity(), Site: "query", Original: cause}
				if e := runtime.Dispatch(scope, notice); e != nil {
					return e
				}
				if e := runtime.Dispatch(scope, notice); e != nil {
					return e
				}
				events, _ := runtime.Events(scope)
				code, _ := runtime.ExitCode(scope, 0)
				if kind == Native {
					if code != 1 || len(events) != 0 || out.String() != "Error: original rejected permission\n" {
						t.Fatal("native decision")
					}
				} else {
					if code != 0 || len(events) != 1 || events[0].Fault.Original != cause || events[0].Promise != child.Identity() {
						t.Fatal("helper diagnostics/identity")
					}
					events[0].Kind = "observer mutation"
					live, _ := runtime.Events(scope)
					if live[0].Kind == "observer mutation" {
						t.Fatal("mutable snapshot")
					}
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

type namedFailure struct{}

func (*namedFailure) Error() string { return "null permission property" }
func (*namedFailure) Name() string  { return "TypeError" }
func TestPolicySinkFailuresIdentityAndScope(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	other := cooperative.New()
	defer other.Close()
	cause := errors.New("actual stderr failure")
	runtime, _ := New(Options{Owner: owner, Kind: ServerHelper, WriteStderr: func([]byte) error { return cause }})
	if runtime.Owns(other, ServerHelper) || runtime.Owns(owner, Native) {
		t.Fatal("wrong process authority")
	}
	for _, o := range []Options{{}, {Owner: owner}, {Owner: owner, Kind: "unknown", WriteStderr: func([]byte) error { return nil }}} {
		if _, e := New(o); e == nil {
			t.Fatal("invalid runtime accepted")
		}
	}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		p, _ := ownerasync.NewOwnerFuture[int](owner)
		q, _ := ownerasync.NewOwnerFuture[int](owner)
		notice := ownerasync.IgnoredRejection{Promise: p.Identity(), Holder: q.Identity(), Site: "success", Original: &namedFailure{}}
		if e := runtime.Dispatch(scope, notice); e != cause {
			t.Fatal("stderr original cause")
		}
		events, _ := runtime.Events(scope)
		if len(events) != 0 {
			t.Fatal("diagnostic after stderr throw")
		}
		r, _ := New(Options{Owner: owner, Kind: CLIHelper, WriteStderr: func([]byte) error { return nil }, Diagnostic: func(Event) error { return cause }})
		if e := r.Dispatch(scope, notice); e != cause {
			t.Fatal("diagnostic original cause")
		}
		if len(r.events) != 1 {
			t.Fatal("real diagnostic queue empty")
		}
		for _, n := range []ownerasync.IgnoredRejection{{}, {Promise: p.Identity(), Holder: p.Identity(), Site: "query", Original: cause}, {Promise: p.Identity(), Holder: q.Identity(), Site: "query", Original: cooperative.ErrClosed}} {
			if e := runtime.Dispatch(scope, n); !errors.Is(e, cooperative.ErrRestricted) {
				t.Fatal("invalid runtime admission")
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := runtime.ExitCode(scope, 0); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign policy")
		}
		if _, e := runtime.Events(scope); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign snapshot")
		}
		if e := runtime.RecordFault(scope, Fault{}); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatal("foreign record")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
