package sessioncontroller

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

func actualOwnedGitService(t *testing.T, state *bootstrapstate.State, owner *cooperative.Owner) *gitbranch.Service {
	t.Helper()
	registry, err := runtimecleanup.New(owner)
	if err != nil {
		t.Fatal(err)
	}
	watch := &gitbranch.NativePollWatch{Stat: os.Stat}
	service, err := gitbranch.New(gitbranch.Options{Owner: owner, CWD: func() string { return state.Snapshot().CWD }, Now: func() int64 { return time.Now().UnixMilli() }, StatSync: os.Stat, Stat: os.Stat, ReadFile: os.ReadFile, WatchFile: watch.Watch, ScrollDraining: func() bool { return false }, RegisterCleanupOwned: func(scope *cooperative.Scope, fn func(*cooperative.Scope) error) error {
		_, e := registry.Register(scope, runtimecleanup.NewCallback(func(next *cooperative.Scope) (*runtimecleanup.Future, error) { return nil, fn(next) }))
		return e
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()); watch.Join() })
	return service
}

func TestOwnedBindingFactoryRejectsForeignBeforeEffects(t *testing.T) {
	c, state, _ := boundController(t)
	foreign := cooperative.New()
	t.Cleanup(foreign.Close)
	service := actualOwnedGitService(t, state, foreign)
	before, e := os.ReadDir(c.options.WriterOptions.CWD)
	if e != nil {
		t.Fatal(e)
	}
	binding, e := NewOwnedGitRecordBranch(state, service)
	var unavailable *CapabilityError
	if !errors.As(e, &unavailable) || binding != nil {
		t.Fatalf("foreign factory returned a capability: binding=%v error=%v", binding, e)
	}
	after, e := os.ReadDir(c.options.WriterOptions.CWD)
	if e != nil {
		t.Fatal(e)
	}
	if c.active || c.writer.path != "" || len(c.messages) != 0 || len(before) != len(after) {
		t.Fatal("binding validation performed activation/Record/file effects")
	}
}

func TestOwnedBindingAdmissionRejectsZeroOrMismatchedBeforeWriter(t *testing.T) {
	c, state, owner := boundController(t)
	service := actualOwnedGitService(t, state, owner)
	binding, err := NewOwnedGitRecordBranch(state, service)
	if err != nil {
		t.Fatal(err)
	}
	another, err := bootstrapstate.New(owner, state.SessionID(), c.options.WriterOptions.CWD, func() (string, error) { return "uninvoked", nil })
	if err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	t.Cleanup(foreign.Close)
	foreignService := actualOwnedGitService(t, state, foreign)
	cases := []*OwnedRecordBranch{nil, {}, {state: state}, {state: another, service: service}, {state: state, service: foreignService}}
	for i, badBinding := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			before, e := os.ReadDir(c.options.WriterOptions.CWD)
			if e != nil {
				t.Fatal(e)
			}
			opts := c.options
			opts.RecordBranchProfile = OwnedGitRecordProfile
			opts.BeginOwnedRecordBranch = badBinding
			opts.AsyncPersistence = true
			opts.WriterOptions.Branch = nil
			candidate, e := New(opts)
			var unavailable *CapabilityError
			if candidate != nil || !errors.As(e, &unavailable) {
				t.Fatalf("invalid binding admitted: %v %v", candidate, e)
			}
			after, e := os.ReadDir(c.options.WriterOptions.CWD)
			if e != nil {
				t.Fatal(e)
			}
			if c.active || c.writer.path != "" || len(c.messages) != 0 || len(before) != len(after) {
				t.Fatal("invalid binding affected startup/Record")
			}
			// Private-field fault injection validates the defensive API invoked by
			// sdkhost.New. This is not an external mutable capability API.
			saved := c.options
			c.options = opts
			e = c.ValidateSDKRecordProfile()
			c.options = saved
			if !errors.As(e, &unavailable) {
				t.Fatal("host profile validation admitted invalid binding", e)
			}
		})
	}
	opts := c.options
	opts.RecordBranchProfile = OwnedGitRecordProfile
	opts.BeginOwnedRecordBranch = binding
	opts.AsyncPersistence = true
	opts.WriterOptions.Branch = nil
	admitted, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	*binding = OwnedRecordBranch{}
	if err = admitted.ValidateSDKRecordProfile(); err != nil {
		t.Fatal("caller zero overwrite changed admitted immutable snapshot", err)
	}
	if admitted.active || admitted.writer.path != "" {
		t.Fatal("valid construction ran activation/Record")
	}
	if err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		token, e := state.Context(scope)
		if e != nil {
			return e
		}
		_, e = (*OwnedRecordBranch)(nil).Begin(scope, token)
		if !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("nil/zero binding Begin was not rejected")
		}
		_, e = (&OwnedRecordBranch{}).Begin(scope, token)
		if !errors.Is(e, cooperative.ErrRestricted) {
			return errors.New("nil/zero binding Begin was not rejected")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
