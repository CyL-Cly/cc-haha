package sessioncontroller

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func boundController(t *testing.T) (*Controller, *bootstrapstate.State, *cooperative.Owner) {
	t.Helper()
	home := t.TempDir()
	options := hostOptions(t, home).options
	owner := cooperative.New()
	t.Cleanup(owner.Close)
	state, err := bootstrapstate.New(owner, options.ResumeOptions.CurrentSessionID, home, func() (string, error) { return "33333333-3333-4333-8333-333333333333", nil })
	if err != nil {
		t.Fatal(err)
	}
	options.Bootstrap = state
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.AwaitBackground)
	return controller, state, owner
}
func TestOwnedActivationSwitchesSharedSIDBeforeReset(t *testing.T) {
	controller, state, owner := boundController(t)
	phases := []string{}
	controller.options.OnPhase = func(value string) { phases = append(phases, value) }
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		cache, _ := state.PlanSlugCache(scope)
		_ = cache.Set(scope, state.SessionID(), "outgoing")
		_, err := state.OnSessionSwitch(scope, bootstrapstate.NewListener(func(next *cooperative.Scope, id string) error {
			phases = append(phases, "listener")
			entries, _ := cache.Entries(next)
			if controller.SessionID() != id || len(entries) != 0 || state.Snapshot().SessionProjectDir != nil {
				t.Fatal("listener saw stale live SID/cache/project")
			}
			return nil
		}))
		if err != nil {
			return err
		}
		return controller.ActivateOwned(scope, activation())
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) < 5 || !reflect.DeepEqual(phases[:5], []string{"resume:start", "resume:return", "switch", "listener", "reset"}) {
		t.Fatal("switch notification moved past reset", phases)
	}
	if controller.sid != "" || controller.SessionID() != "11111111-1111-4111-8111-111111111111" {
		t.Fatal("controller maintained a second live authority", controller.sid, controller.SessionID())
	}
	if !controller.BootstrapOwns(owner) {
		t.Fatal("startup owner not retained")
	}
}
func TestOwnedActivationCannotBypassOwnerOrAcceptOtherAuthority(t *testing.T) {
	controller, state, owner := boundController(t)
	phases := []string{}
	controller.options.OnPhase = func(value string) { phases = append(phases, value) }
	if err := controller.Activate(activation()); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		return controller.ActivateOwned(scope, activation())
	}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if len(phases) != 0 || controller.active || state.SessionID() != controller.options.ResumeOptions.CurrentSessionID {
		t.Fatal("rejected owner had effects", phases)
	}
	if controller.BootstrapOwns(foreign) {
		t.Fatal("foreign SDK owner accepted")
	}
	bad := controller.options
	bad.ResumeOptions.CurrentSessionID = "other"
	if _, err := New(bad); err == nil {
		t.Fatal("mismatched startup SID accepted")
	}
	other, _ := bootstrapstate.New(owner, state.SessionID(), t.TempDir(), func() (string, error) { return "other", nil })
	bad = controller.options
	bad.WriterOptions.Bootstrap = other
	if _, err := New(bad); err == nil {
		t.Fatal("two authority objects accepted")
	}
}
func TestOwnedSwitchThrowRetainsStateAndDoesNotResetWriter(t *testing.T) {
	controller, state, owner := boundController(t)
	original := errors.New("original listener")
	retained := filepath.Join(t.TempDir(), "old.jsonl")
	controller.writer.path = retained
	controller.writer.pending = []*protocol.JSONObject{new(protocol.JSONObject)}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _ = state.OnSessionSwitch(scope, bootstrapstate.NewListener(func(*cooperative.Scope, string) error { return original }))
		return controller.ActivateOwned(scope, activation())
	})
	if err != original || state.SessionID() != "11111111-1111-4111-8111-111111111111" || controller.writer.path != retained || len(controller.writer.pending) != 1 || controller.active {
		t.Fatal("throw rolled back SID or executed later startup", err)
	}
}
func TestOwnedForkDoesNotSwitchAndLiveRegenerateReachesWriter(t *testing.T) {
	controller, state, owner := boundController(t)
	initial := state.SessionID()
	calls := 0
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _ = state.OnSessionSwitch(scope, bootstrapstate.NewListener(func(*cooperative.Scope, string) error { calls++; return nil }))
		input := activation()
		input.ForkSession = true
		if err := controller.ActivateOwned(scope, input); err != nil {
			return err
		}
		if state.SessionID() != initial || calls != 0 {
			t.Fatal("fork switched existing session")
		}
		cached := filepath.Join(t.TempDir(), "cached.jsonl")
		controller.writer.path = cached
		generated, err := state.RegenerateSessionID(scope, true)
		if err != nil {
			return err
		}
		if controller.SessionID() != generated || controller.writer.currentSessionID() != generated || filepath.Base(controller.writer.target()) != generated+".jsonl" || controller.writer.path != cached || calls != 0 {
			return errors.New("live SID/path pointer lifecycle changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
