package sessioncontroller

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"os"
	"reflect"
	"strings"
	"testing"
)

func publicController(t *testing.T) (*Controller, *bootstrapstate.State, *cooperative.Owner) {
	t.Helper()
	base, state, owner := boundController(t)
	opts := base.options
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: opts.ResumeOptions.CWD, ConfigDir: &opts.ResumeOptions.ConfigDir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = global.Close() })
	settings, e := clisettings.NewService(clisettings.Options{State: opts.ResumeOptions.SettingsState, ConfigHome: opts.ResumeOptions.ConfigDir, OriginalCWD: opts.ResumeOptions.OriginalCWD, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1700000000000 }, PID: os.Getpid()})
	if e != nil {
		t.Fatal(e)
	}
	env, _ := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(opts.ResumeOptions.CWD)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(opts.ResumeOptions.ConfigDir)}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}, protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}))
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: opts.ResumeOptions.SettingsState, Environment: env, ConfigHome: opts.ResumeOptions.ConfigDir, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		t.Fatal(e)
	}
	opts.PublicResumeStartup = startup
	opts.AsyncPersistence = true
	c, e := New(opts)
	if e != nil {
		t.Fatal(e)
	}
	return c, state, owner
}
func TestOwnedActivationPublicLoadSwitchAwaitResetRestore(t *testing.T) {
	c, state, o := publicController(t)
	phases := []string{}
	c.options.OnPhase = func(v string) { phases = append(phases, v) }
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := state.OnSessionSwitch(scope, bootstrapstate.NewListener(func(next *cooperative.Scope, id string) error {
			phases = append(phases, "listener")
			if c.active || state.SessionID() != id {
				t.Error("early restore/stale SID")
			}
			_, e := o.Microtask(cooperative.Read, func(*cooperative.Scope) error { phases = append(phases, "switch-promise-microtask"); return nil })
			return e
		}))
		if e != nil {
			return e
		}
		f, e := c.BeginActivate(scope, activation())
		if e != nil {
			return e
		}
		if _, e = c.BeginActivate(scope, activation()); e == nil {
			t.Error("concurrent activation was admitted")
		}
		if _, e = f.Await(scope); e != nil {
			return e
		}
		if !c.active || c.activationPending || c.SessionID() != "11111111-1111-4111-8111-111111111111" {
			t.Error("activation did not finish", c.active, c.SessionID())
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	expected := []string{"resume:start", "resume:return", "switch", "listener", "reset", "switch-promise-microtask", "state:restore", "metadata:restore", "read-cache:return"}
	if !reflect.DeepEqual(phases, expected) {
		t.Fatal("public reset's real await was lost", phases)
	}
	if c.writer.async == nil || c.writer.async.owner != o || c.writer.async.hostClaimed {
		t.Fatal("activation allocated/claimed a second Writer")
	}
}
func TestOwnedActivationListenerThrowKeepsSIDAndSkipsReset(t *testing.T) {
	c, state, o := publicController(t)
	reason := errors.New("original listener object")
	phase := []string{}
	c.options.OnPhase = func(v string) { phase = append(phase, v) }
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := state.OnSessionSwitch(scope, bootstrapstate.NewListener(func(*cooperative.Scope, string) error { return reason }))
		if e != nil {
			return e
		}
		f, e := c.BeginActivate(scope, activation())
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		if e != reason || c.active || c.activationPending {
			t.Error("listener identity swallowed", e)
		}
		if state.SessionID() != "11111111-1111-4111-8111-111111111111" {
			t.Error("switch rolled back")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(phase, []string{"resume:start", "resume:return", "switch"}) {
		t.Fatal(phase)
	}
}
func TestOwnedActivationForkAndPersistenceDisabledHaveNoInventedReset(t *testing.T) {
	for _, fork := range []bool{true, false} {
		t.Run(map[bool]string{true: "fork", false: "disabled"}[fork], func(t *testing.T) {
			c, state, o := publicController(t)
			if !fork {
				c.options.WriterOptions.SessionPersistenceDisabled = true
			}
			phases := []string{}
			c.options.OnPhase = func(v string) { phases = append(phases, v) }
			before := state.SessionID()
			if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
				input := activation()
				input.ForkSession = fork
				f, e := c.BeginActivate(scope, input)
				if e != nil {
					return e
				}
				_, e = f.Await(scope)
				return e
			}); e != nil {
				t.Fatal(e)
			}
			for _, p := range phases {
				if p == "reset" {
					t.Fatal("invented await/reset", phases)
				}
			}
			if fork && state.SessionID() != before {
				t.Fatal("fork switched SID")
			}
		})
	}
}
func TestOwnedActivationErrorsAndForeignOwnerRemainAdministrative(t *testing.T) {
	c, _, o := publicController(t)
	foreign := cooperative.New()
	defer foreign.Close()
	if e := foreign.Run(context.Background(), func(scope *cooperative.Scope) error { _, e := c.BeginActivate(scope, activation()); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := c.BeginActivate(nil, activation()); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, input := range []Activation{{Resume: resumestate.Input{Source: []byte("null")}}, {ResumeAt: "absent", Resume: activation().Resume}, {Resume: resumestate.Input{Source: []byte(`{"messages":null}`)}}} {
			f, e := c.BeginActivate(scope, input)
			if e != nil {
				return e
			}
			_, e = f.Await(scope)
			if e == nil || c.active || c.activationPending {
				t.Error("async rejection did not settle/release pending", e)
			}
		}
		f, e := c.BeginActivate(scope, activation())
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		if e != nil {
			return e
		}
		if _, e = c.BeginActivate(scope, activation()); e == nil {
			t.Error("duplicate finished activation")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestOwnedActivationNoLegacyContextConstructionFallback(t *testing.T) {
	c, _, _ := publicController(t)
	opts := c.options
	opts.UserContext = nil
	if _, e := New(opts); e == nil || !strings.Contains(e.Error(), "context service binding") {
		t.Fatal("public constructor read policy as fallback", e)
	}
	opts = c.options
	opts.AsyncPersistence = false
	legacy, e := New(opts)
	if e != nil {
		t.Fatal(e)
	}
	// Explicitly unsupported owner dependency cannot use legacy Load off-owner.
	if _, e = legacy.BeginActivate(nil, activation()); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
}
