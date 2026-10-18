package clibootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSDKSettingsBindingRequiresActualFactoryOwnerAndOriginalReader(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	makeMain := func() *MainPrint {
		f := newNativePluginFixture(t)
		f.n.Environment["ANTHROPIC_API_KEY"] = "offline-sdk-reader-binding"
		f.write(t, f.config+"/settings.json", `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`)
		disabled := false
		m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, MaxTurns: 1, Tools: []string{""}, Sources: State{AllowedSettingSources: []string{"userSettings"}, EnabledSettingSources: []string{"userSettings"}}}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
		if err := m.StartSession(ctx); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m, other := makeMain(), makeMain()
	foreignReader, err := other.Startup.BindNativeSDKSettingsReader(other.settings, other.settingState, other.environment)
	if err != nil {
		t.Fatal(err)
	}
	var retained *cooperative.Scope
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		retained = scope
		for _, reader := range []*clistartupenv.NativeSDKSettingsReader{nil, new(clistartupenv.NativeSDKSettingsReader), foreignReader} {
			if err := m.Host.BindNativeSDKSettingsReader(scope, reader); !errors.Is(err, cooperative.ErrRestricted) {
				t.Errorf("missing, fabricated or foreign reader admitted: %v", err)
			}
		}
		m.options.Interactive = true
		if err := m.bindNativeSDKConfigSettings(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("interactive caller acquired the SDK directory reader", err)
		}
		m.options.Interactive = false
		m.agentChild = true
		if err := m.bindNativeSDKConfigSettings(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("Agent child acquired the root SDK directory reader", err)
		}
		m.agentChild = false
		if err := m.bindNativeSDKConfigSettings(scope); err != nil {
			return err
		}
		reader := m.nativeSDKConfigReader()
		if err := m.Host.BindNativeSDKSettingsReader(scope, reader); err != nil {
			return err
		}
		another, err := m.Startup.BindNativeSDKSettingsReader(m.settings, m.settingState, m.environment)
		if err != nil {
			return err
		}
		if err := m.Host.BindNativeSDKSettingsReader(scope, another); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("replacement reader changed an admitted Host binding", err)
		}
		before, err := m.nativeSDKInitialSettings()
		if err != nil {
			return err
		}
		next := filepath.Join(m.options.Native.Home, "sdk-reader-next")
		if err := os.MkdirAll(next, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(next, "settings.json"), []byte(`{"future":{"keep":"NEXT"}}`), 0600); err != nil {
			return err
		}
		if err := m.applyNativeSDKEnvironment(scope, protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: rawMain(next)})); err != nil {
			return err
		}
		if _, err := m.nativeSDKSettingsWithSources(); err != nil {
			return err
		}
		// A detached JSON clone cannot substitute for the original AppState
		// reference even after the real SDK reader has been admitted.
		manual := before.Clone()
		if err := m.Host.AcceptNativeSettingsReadRefresh(scope, &manual); err != nil {
			return err
		}
		if _, err := m.Host.TransferNativeSettings(scope); err == nil {
			t.Error("manual JSON certified the changed source cache")
		}
		if err := m.Host.AcceptNativeSettingsReadRefresh(scope, before); err != nil {
			return err
		}
		if _, err := m.Host.TransferNativeSettings(scope); err != nil {
			return err
		}
		fresh, err := m.nativeSDKInitialSettings()
		if err != nil {
			return err
		}
		original := append([]byte(nil), fresh.Get("future")...)
		fresh.Set("future", []byte(`{"keep":"FORGED"}`))
		if _, err := m.Host.TransferNativeSettings(scope); err == nil {
			t.Error("mutated shared JSON bypassed the captured cache certificate")
		}
		fresh.Set("future", original)
		if _, err := m.Host.TransferNativeSettings(scope); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reader := m.nativeSDKConfigReader()
	if err := m.bindNativeSDKConfigSettings(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Error("retained Main scope admitted", err)
	}
	if err := m.Host.BindNativeSDKSettingsReader(retained, reader); !errors.Is(err, cooperative.ErrRestricted) {
		t.Error("retained Host scope admitted", err)
	}
	if err := other.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.Host.BindNativeSDKSettingsReader(scope, reader); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign active owner admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.Host.Shutdown(scope); err != nil {
			return err
		}
		if err := m.Host.BindNativeSDKSettingsReader(scope, reader); !errors.Is(err, cooperative.ErrClosed) {
			t.Error("closed Host rebound settings reader", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
