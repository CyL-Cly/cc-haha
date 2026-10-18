package clistartupenv

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePolicyPreparationAfterTrustedEnvironmentBeforePolicy(t *testing.T) {
	base, _ := newTestService(t, `{"env":{"ANTHROPIC_MODEL":"global"}}`, protocol.NewJSONObject(), true)
	if err := os.WriteFile(filepath.Join(base.options.ConfigHome, "settings.json"), []byte(`{"env":{"ANTHROPIC_MODEL":"trusted-user","ANTHROPIC_BASE_URL":"https://custom.invalid"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	options := base.options
	options.Host = Host{Profile: ResolvedPolicyReleaseProfile, PluginBaseAbsent: true, PrepareNativePolicy: func() error {
		order = append(order, "prepare")
		if !equalString(options.Environment.Get("ANTHROPIC_MODEL"), "trusted-user") || !equalString(options.Environment.Get("ANTHROPIC_BASE_URL"), "https://custom.invalid") {
			return errors.New("eligibility sampled before trusted settings")
		}
		return nil
	}, ResolvePolicy: func() (*protocol.JSONObject, error) {
		order = append(order, "policy")
		object := parseObject(`{"env":{"ANTHROPIC_MODEL":"policy"}}`)
		return &object, nil
	}}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplySafeConfigEnvironmentVariables(); err != nil {
		t.Fatal(err)
	}
	prepare, policy := -1, -1
	for index, event := range order {
		if event == "prepare" {
			prepare = index
		}
		if event == "policy" && prepare >= 0 && policy < 0 {
			policy = index
		}
	}
	if prepare < 0 || policy <= prepare || !equalString(options.Environment.Get("ANTHROPIC_MODEL"), "policy") {
		t.Fatal("actual safe-init precedence changed", order, options.Environment.Snapshot())
	}
}
func TestNativePolicyPreparationErrorIdentityAndFiniteNilContract(t *testing.T) {
	base, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	original := errors.New("policy preparation failed")
	options := base.options
	policyReads := 0
	options.Host = Host{Profile: ResolvedPolicyReleaseProfile, PluginBaseAbsent: true, PrepareNativePolicy: func() error { return original }, ResolvePolicy: func() (*protocol.JSONObject, error) { policyReads++; return nil, nil }}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplySafeConfigEnvironmentVariables(); !errors.Is(err, original) {
		t.Fatal("preparation error identity changed", err)
	}
	// No post-preparation policy read can bypass the failed eligibility producer.
	if policyReads != 0 {
		t.Fatal("failed preparation still read policy", policyReads)
	}
	options.Host.PrepareNativePolicy = nil
	service, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplySafeConfigEnvironmentVariables(); err != nil {
		t.Fatal("existing native nil hook changed", err)
	}
	finite, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	before := finite.options.Environment.Snapshot()
	if err = finite.ApplySafeConfigEnvironmentVariables(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, finite.options.Environment.Snapshot()) {
		t.Fatal("existing finite init changed")
	}
}
