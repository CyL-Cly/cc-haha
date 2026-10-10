package sdkpermissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNativePlanAutoPolicyTrustAndRestoration(t *testing.T) {
	for _, row := range []struct {
		name    string
		sources map[string]string
		merged  string
		want    bool
	}{
		{"user-opt-in", map[string]string{"userSettings": `{"skipAutoPermissionPrompt":true}`}, `{}`, true},
		{"project-is-not-consent", map[string]string{"projectSettings": `{"skipAutoPermissionPrompt":true}`}, `{}`, false},
		{"local-veto", map[string]string{"userSettings": `{"skipAutoPermissionPrompt":true}`, "localSettings": `{"useAutoModeDuringPlan":false}`}, `{}`, false},
		{"managed-disable", map[string]string{"userSettings": `{"skipAutoPermissionPrompt":true}`}, `{"permissions":{"disableAutoMode":"disable"}}`, false},
		{"trusted-shell-rules", map[string]string{"flagSettings": `{"skipAutoPermissionPrompt":true,"autoMode":{"classifyAllShell":true}}`}, `{}`, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				var merged protocol.JSONObject
				_ = merged.UnmarshalJSON([]byte(row.merged))
				service := &NativeStartup{owner: owner, options: NativeStartupOptions{SettingsForSource: func(name string) (*protocol.JSONObject, error) {
					if value, ok := row.sources[name]; ok {
						var r protocol.JSONObject
						_ = r.UnmarshalJSON([]byte(value))
						return &r, nil
					}
					return nil, nil
				}, InitialSettings: func() (*protocol.JSONObject, error) { return &merged, nil }}}
				var original protocol.JSONObject
				_ = original.UnmarshalJSON([]byte(`{"mode":"default","future":"retained","alwaysAllowRules":{"session":["Bash(python:*)","Read"],"policySettings":["Bash(*)"]}}`))
				s := &State{owner: owner, nativeStartup: service, nativeProcess: true, context: original}
				if e := s.EnterPlanMode(scope); e != nil {
					return e
				}
				active, e := s.AutoModeActive(scope)
				if e != nil {
					return e
				}
				if active != row.want {
					return fmt.Errorf("active=%v wanted=%v", active, row.want)
				}
				if row.want {
					a, _ := s.context.Object("alwaysAllowRules")
					if string(a.Get("session")) != `["Read"]` || string(a.Get("policySettings")) != `["Bash(*)"]` {
						return fmt.Errorf("unsafe bypass or managed rule mutated: %s", autoRaw(a))
					}
				}
				if _, e = s.ExitPlanMode(scope); e != nil {
					return e
				}
				a, _ := s.context.Object("alwaysAllowRules")
				var list []string
				_ = json.Unmarshal(a.Get("session"), &list)
				if s.context.String("mode") != "default" || s.context.String("future") != "retained" || s.context.Get("strippedDangerousRules") != nil || len(list) != 2 {
					return fmt.Errorf("restored=%s", autoRaw(s.context))
				}
				if _, e := s.AutoModeActive(nil); !errors.Is(e, cooperative.ErrRestricted) {
					return fmt.Errorf("nil owner scope admitted: %v", e)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativePlanAutoPolicySettingsReadFailureBeforeMutation(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	sentinel := errors.New("private setting unavailable")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var original protocol.JSONObject
		_ = original.UnmarshalJSON([]byte(`{"mode":"default","alwaysAllowRules":{"session":["Bash(*)"]}}`))
		before := autoRaw(original)
		s := &State{owner: owner, nativeProcess: true, context: original, nativeStartup: &NativeStartup{options: NativeStartupOptions{SettingsForSource: func(string) (*protocol.JSONObject, error) { return nil, sentinel }, InitialSettings: func() (*protocol.JSONObject, error) {
			return nil, fmt.Errorf("continued after failed read")
		}}}}
		if e := s.EnterPlanMode(scope); e != sentinel {
			return fmt.Errorf("error identity: %v", e)
		}
		if string(before) != string(autoRaw(s.context)) {
			return fmt.Errorf("failed entry changed state")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativePlanAutoPolicyReloadDoesNotResurrectDeletedDiskRules(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		empty := protocol.NewJSONObject()
		service := &NativeStartup{options: NativeStartupOptions{SettingsForSource: func(string) (*protocol.JSONObject, error) { return &empty, nil }}}
		var original protocol.JSONObject
		_ = original.UnmarshalJSON([]byte(`{"mode":"plan","alwaysAllowRules":{"userSettings":["Bash(node:*)","Read"],"session":["Bash(ssh:*)"]},"strippedDangerousRules":{"userSettings":["Bash(python:*)"],"session":["Bash(bun:*)"]}}`))
		s := &State{owner: owner, nativeProcess: true, nativeStartup: service, context: original, autoModeActive: true}
		if e := s.reconcileAutoRules(); e != nil {
			return e
		}
		if e := s.restoreAutoRules(); e != nil {
			return e
		}
		a, _ := s.context.Object("alwaysAllowRules")
		if string(a.Get("userSettings")) != `["Read","Bash(node:*)"]` || string(a.Get("session")) != `["Bash(bun:*)","Bash(ssh:*)"]` {
			return fmt.Errorf("old disk rule resurrected or session stash lost: %s", autoRaw(a))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
