package sessioncontroller

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSDKSettingsStoreSharesActualConsumers(t *testing.T) {
	c, bootstrap, owner := boundController(t)
	home := c.options.ResumeOptions.OriginalCWD
	config := c.options.ResumeOptions.ConfigDir
	state, _ := clisettings.NewState(func() float64 { return 0 })
	settings, e := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 0 }, PID: os.Getpid()})
	if e != nil {
		t.Fatal(e)
	}
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
	if e != nil {
		t.Fatal(e)
	}
	if e = global.Enable(); e != nil {
		t.Fatal(e)
	}
	defer global.Close()
	environment, _ := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(config)}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: state, Environment: environment, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		t.Fatal(e)
	}
	contextService, e := usercontext.New(usercontext.Options{Environment: environment, Settings: settings, SettingsState: state, Now: c.options.Now, Profile: usercontext.ExternalProfile})
	if e != nil {
		t.Fatal(e)
	}
	c.options.UserContext = contextService
	c.options.PublicResumeStartup = startup
	c.options.WriterOptions.SettingsState = state
	c.options.ResumeOptions.SettingsState = state
	if !startup.SharesSettings(settings, state, environment) || !c.UsesOriginalSettingsContext(startup, settings, state, environment) {
		t.Fatal("real shared bindings not admitted")
	}
	alternate, _ := clisettings.NewState(func() float64 { return 0 })
	if c.UsesSettingsState(alternate) || c.UsesOriginalSettingsContext(startup, settings, alternate, environment) || startup.SharesSettings(settings, alternate, environment) {
		t.Fatal("second settings/cache authority admitted")
	}
	alternateEnv, _ := clistartupenv.NewEnvironment(environment.Snapshot())
	if contextService.SharesSettings(settings, state, alternateEnv) {
		t.Fatal("equal bytes hid second env authority")
	}
	cache, e := startup.GetInitialSettings()
	if e != nil {
		t.Fatal(e)
	}
	gate, _ := workflowcommands.NewGate(owner, startup, environment)
	var permission *sdkpermissions.State
	if e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		permission, e = sdkpermissions.Initialize(scope, owner, cache, gate, sdkpermissions.Input{Profile: sdkpermissions.NeutralBareProfile, Platform: "darwin", Model: "fixture-model", NonInteractive: true, BaseToolsCLI: []string{""}, Mode: "default"})
		if e != nil {
			return e
		}
		if e = c.InitializeSDKSettings(scope, permission); e != nil {
			return e
		}
		live, context, e := c.SDKSettingsOnOwner(scope)
		if e != nil {
			return e
		}
		if live != cache || c.appState.settings != state.GetSessionSettingsCache().Settings || context.String("mode") != "default" {
			return fmt.Errorf("%s", fmt.Sprint("AppState actual producer detached"))
		}
		if e = c.InitializeSDKSettings(scope, permission); e == nil {
			return fmt.Errorf("%s", fmt.Sprint("double store init"))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if e = foreign.Run(context.Background(), func(s *cooperative.Scope) error { _, _, e := c.SDKSettingsOnOwner(s); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("foreign scope state read", e)
	}
	if e = c.InitializeSDKSettings(nil, permission); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	var nilController *Controller
	if _, _, e = nilController.SDKSettingsOnOwner(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if nilController.UsesSettingsState(state) || nilController.UsesOriginalSettingsContext(startup, settings, state, environment) {
		t.Fatal("nil binding")
	}
	empty := &Controller{}
	if _, _, e = empty.SDKSettingsOnOwner(nil); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e = empty.InitializeSDKSettings(nil, permission); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	_ = bootstrap
	if _, e = os.Stat(filepath.Join(config, "settings.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("store initializer unexpectedly wrote settings", e)
	}
}
func TestSDKSettingsUnavailableBeforeInitialization(t *testing.T) {
	c, _, owner := boundController(t)
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		_, _, e := c.SDKSettingsOnOwner(s)
		var cap *CapabilityError
		if !errors.As(e, &cap) {
			return fmt.Errorf("%s", fmt.Sprint("missing real initializer silently defaulted", e))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
