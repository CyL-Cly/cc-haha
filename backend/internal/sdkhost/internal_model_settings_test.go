package sdkhost

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func modelSettings(raw string) *protocol.JSONObject {
	var object protocol.JSONObject
	if err := object.UnmarshalJSON([]byte(raw)); err != nil {
		panic(err)
	}
	return &object
}

func TestInternalModelCacheKeepsFiniteIdentityAndRejectsOtherConsumers(t *testing.T) {
	before := modelSettings(`{"model":"old","effortLevel":"medium","permissions":{"allow":["Read"]},"future":{"utf16":"\ud800"}}`)
	next := modelSettings(`{"model":"opus","effortLevel":"low","permissions":{"allow":["Read"]},"future":{"utf16":"\ud800"}}`)
	host := &Host{options: Options{Print: &PrintOptions{NativeProcess: true}}, internalModelSettings: next}
	if err := host.validateASKSettingsReference(before, next); err != nil {
		t.Fatal(err)
	}
	clone := next.Clone()
	if err := host.validateASKSettingsReference(before, &clone); err == nil {
		t.Fatal("unknown replacement cache admitted only by equal values")
	}
	host.options.Print.NativeProcess = false
	if err := host.validateASKSettingsReference(before, next); err == nil {
		t.Fatal("finite cache reference guard broadened")
	}
	host.options.Print.NativeProcess = true
	for _, raw := range []string{
		`{"model":"opus","permissions":{"allow":["Write"]},"future":{"utf16":"\ud800"}}`,
		`{"model":"opus","permissions":{"allow":["Read"]},"future":{"utf16":"�"}}`,
		`{"model":"opus","permissions":{"allow":["Read"]},"future":{"utf16":"\ud800"},"language":"new"}`,
		`{"model":"opus","permissions":{"allow":["Read"]}}`,
		`{"model":"opus","effortLevel":"max","permissions":{"allow":["Read"]},"future":{"utf16":"\ud800"}}`,
	} {
		candidate := modelSettings(raw)
		host.internalModelSettings = candidate
		if err := host.validateASKSettingsReference(before, candidate); err == nil {
			t.Fatalf("unconsumed setting change admitted: %s", raw)
		}
	}
}

func TestInternalModelCacheRequiresOriginalServicesAndActualOwner(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	host := &Host{options: Options{Owner: owner, Print: &PrintOptions{NativeProcess: true}}}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := host.AcceptInternalModelSettings(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign settings cache transition: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := host.AcceptInternalModelSettings(scope); err == nil {
			return fmt.Errorf("settings transition accepted a stub without shared services")
		}
		host.options.Print.NativeProcess = false
		if err := host.AcceptInternalModelSettings(scope); err == nil {
			return fmt.Errorf("finite transition admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestClearedSettingsLifecycleCannotAdmitOldFastCache(t *testing.T) {
	before := modelSettings(`{"model":"old","fastMode":true,"permissions":{"allow":["Read"]},"future":"\ud800"}`)
	removed := modelSettings(`{"model":"old","permissions":{"allow":["Read"]},"future":"\ud800"}`)
	host := &Host{options: Options{Print: &PrintOptions{NativeProcess: true}}, internalFastSettings: removed}
	if err := host.validateASKSettingsReference(before, removed); err != nil {
		t.Fatal(err)
	}
	host.clearInternalSettingsCertificate()
	if err := host.validateASKSettingsReference(before, removed); err == nil {
		t.Fatal("a retired fast-mode cache bypassed the next settings lifecycle")
	}
}
