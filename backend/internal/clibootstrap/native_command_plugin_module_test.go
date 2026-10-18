package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
)

func TestNativePluginNamespaceActualFactoryImportsRunOriginalHandler(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Error("namespace imports must not invent physical transport")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer peer.Close()
	m := nativeCommandInitFixture(t, peer.URL)
	market := filepath.Join(m.Native.Home, "market")
	expected, err := json.MarshalIndent([]struct {
		Name            string `json:"name"`
		Source          string `json:"source"`
		Path            string `json:"path"`
		InstallLocation string `json:"installLocation"`
	}{{"mkt", "directory", market, market}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var original *nativePluginNamespace
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		one, err := m.importNativePluginNamespace(scope)
		if err != nil {
			return err
		}
		two, err := m.importNativePluginNamespace(scope)
		if err != nil {
			return err
		}
		if one == two {
			t.Error("actual command imports reused a public Promise")
		}
		original, err = one.Await(scope)
		if err != nil {
			return err
		}
		second, err := two.Await(scope)
		if err != nil {
			return err
		}
		if second != original || original.command != m || original.binding != m.nativeCommandInit {
			t.Error("actual factory changed its captured handler namespace")
		}
		result, err := m.PluginCommand(context.Background(), scope, plugincli.Command{Action: "marketplace-list", JSON: true})
		if err != nil {
			return err
		}
		if result.Stdout != string(expected)+"\n" || result.Stderr != "" || result.ExitCode != 0 {
			t.Error("actual imported marketplace handler changed its result", result)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if m.nativeAPIPreconnect.fired {
		t.Fatal("handler import acquired the separate Commander preAction")
	}
	if count, _ := m.nativeInitRefs.Snapshot(); count != 0 {
		t.Fatal("actual command handler left native references", count)
	}
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		pending, err := m.importNativePluginNamespace(scope)
		if err != nil {
			return err
		}
		got, err := pending.Await(scope)
		if err == nil && got != original {
			t.Error("next real command turn replaced a cached namespace")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativePluginNamespaceRejectsManualForeignRetiredAndClosedFactories(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Error("invalid namespace acquired real transport")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer peer.Close()
	m := nativeCommandInitFixture(t, peer.URL)
	foreign := cooperative.New()
	defer foreign.Close()
	manual := &MainCommand{Owner: m.Owner, Native: m.Native, Settings: m.Settings}
	var namespace *nativePluginNamespace
	var expired *cooperative.Scope
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		expired = scope
		if _, err := manual.importNativePluginNamespace(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("manual facade forged native module factory", err)
		}
		pending, err := m.importNativePluginNamespace(scope)
		if err != nil {
			return err
		}
		namespace, err = pending.Await(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.importNativePluginNamespace(expired); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("expired module scope admitted", err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := m.importNativePluginNamespace(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign module import admitted", err)
		}
		if _, err := namespace.invoke(context.Background(), scope, plugincli.Command{Action: "marketplace-list", JSON: true}); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("foreign namespace invocation admitted", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(context.Background(), func(scope *cooperative.Scope) error {
		original := m.nativeCommandInit
		m.nativeCommandInit = &nativeCommandInitBinding{owner: m.Owner, native: m.Native, settings: m.Settings, environment: m.Settings.environment}
		_, err := namespace.invoke(context.Background(), scope, plugincli.Command{Action: "marketplace-list", JSON: true})
		m.nativeCommandInit = original
		if !errors.Is(err, cooperative.ErrRestricted) {
			t.Error("retired namespace reused a substituted factory token", err)
		}
		m.nativeAPIPreconnect.life.close()
		if _, err := m.importNativePluginNamespace(scope); !errors.Is(err, cooperative.ErrClosed) {
			t.Error("closed actual command admitted a late module import", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
