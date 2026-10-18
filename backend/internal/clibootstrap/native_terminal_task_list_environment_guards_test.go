package clibootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeTaskListEnvironmentBindingKeepsConcreteIdentityAndScopeGuards(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Error("Task environment binding invoked a model", request.Method)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) { options.Interactive = true })
	defer m.Close()
	other := backgroundAgentMainWithOptions(t, peer.URL, nil, func(options *MainPrintOptions) { options.Interactive = true })
	defer other.Close()
	clone, err := clistartupenv.NewEnvironment(m.environment.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	state, err := clisettings.NewState(func() float64 { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	finite, err := clistartupenv.New(clistartupenv.Options{Global: m.Global, Settings: m.settings, SettingsState: m.settingState, Environment: m.environment, ConfigHome: m.options.Native.ConfigHomeDirectory(), Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if err != nil {
		t.Fatal(err)
	}
	if finite.OwnsNativeTaskEnvironment(m.settings, m.settingState, m.environment) {
		t.Fatal("Finite startup profile became a native environment producer")
	}
	bind := m.Controller.BindNativeTaskEnvironmentOnOwner
	if err := bind(nil, m.Startup, m.settings, m.settingState, m.environment); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Absent scope admitted", err)
	}
	var expired *cooperative.Scope
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		expired = scope
		for name, call := range map[string]func() error{
			"nil environment":           func() error { return bind(scope, m.Startup, m.settings, m.settingState, nil) },
			"equal-value environment":   func() error { return bind(scope, m.Startup, m.settings, m.settingState, clone) },
			"foreign settings state":    func() error { return bind(scope, m.Startup, m.settings, state, m.environment) },
			"finite startup":            func() error { return bind(scope, finite, m.settings, m.settingState, m.environment) },
			"foreign concrete services": func() error { return bind(scope, other.Startup, other.settings, other.settingState, other.environment) },
		} {
			if err := call(); !errors.Is(err, cooperative.ErrRestricted) {
				t.Errorf("%s admitted alternate authority: %v", name, err)
			}
		}
		if err := bind(scope, m.Startup, m.settings, m.settingState, m.environment); err != nil {
			return err
		}
		if err := bind(scope, m.Startup, m.settings, m.settingState, m.environment); err != nil {
			return err
		}
		if err := bind(scope, other.Startup, other.settings, other.settingState, other.environment); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("Retained binding was rebound to another actual process", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bind(expired, m.Startup, m.settings, m.settingState, m.environment); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Expired scope admitted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		if err := bind(scope, m.Startup, m.settings, m.settingState, m.environment); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("Foreign actual Owner scope admitted task environment")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	if err := bind(expired, m.Startup, m.settings, m.settingState, m.environment); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("Closed process admitted task environment", err)
	}
}
