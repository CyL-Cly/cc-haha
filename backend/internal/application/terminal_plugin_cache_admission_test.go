package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func TestActualPluginCacheEditTicketRejectsForeignReplayMutatedAndClosedOwner(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("cache admission opened a model", r.URL)
		w.WriteHeader(500)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_CODE_SIMPLE"] = "1"
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var ticket *sdkhost.NativePluginCacheEdit
	var retained *cooperative.Scope
	if err := s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		retained = scope
		var err error
		ticket, err = s.process.Host.BeginNativePluginCacheEdit(scope)
		if err != nil {
			return err
		}
		before, permissions, err := s.process.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return err
		}
		if err := s.process.Host.CompleteNativePluginCacheEdit(scope, ticket); err != nil {
			return err
		}
		after, nextPermissions, err := s.process.Controller.SDKSettingsOnOwner(scope)
		if err != nil {
			return err
		}
		permissionJSON, _ := permissions.MarshalJSON()
		nextPermissionJSON, _ := nextPermissions.MarshalJSON()
		if before != after || !bytes.Equal(permissionJSON, nextPermissionJSON) {
			return fmt.Errorf("cache edit replaced actual AppState settings or finite permission value: settingsSame=%v before=%s after=%s", before == after, permissionJSON, nextPermissionJSON)
		}
		if err := s.process.Host.CompleteNativePluginCacheEdit(scope, ticket); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("replayed producer ticket: %v", err)
		}
		if err := s.process.Host.CompleteNativePluginCacheEdit(scope, &sdkhost.NativePluginCacheEdit{}); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("unissued ticket: %v", err)
		}
		mutated, err := s.process.Host.BeginNativePluginCacheEdit(scope)
		if err != nil {
			return err
		}
		actual, err := s.process.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		actual.Set("enabledPlugins", []byte(`{"malicious-in-place@future":true}`))
		if err := s.process.Host.CompleteNativePluginCacheEdit(scope, mutated); err == nil {
			return fmt.Errorf("in-place mutation of even an allowed field admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.process.Host.BeginNativePluginCacheEdit(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained owner scope", err)
	}
	if _, err := s.process.Host.BeginNativePluginCacheEdit(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil owner scope", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := s.process.Host.BeginNativePluginCacheEdit(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign owner: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.process.Host.BeginNativePluginCacheEdit(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("closed/retained owner", err)
	}
	var absent *sdkhost.Host
	if _, err := absent.BeginNativePluginCacheEdit(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil host", err)
	}
}
