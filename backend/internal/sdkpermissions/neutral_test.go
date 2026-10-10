package sdkpermissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func setup(t *testing.T, content string) (*cooperative.Owner, *protocol.JSONObject, *workflowcommands.Gate) {
	t.Helper()
	h := t.TempDir()
	config := filepath.Join(h, ".claude")
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if content != "" {
		if e := os.WriteFile(filepath.Join(config, "settings.json"), []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	state, _ := clisettings.NewState(func() float64 { return 0 })
	settings, e := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: h, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 0 }, PID: os.Getpid()})
	if e != nil {
		t.Fatal(e)
	}
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: h, ConfigDir: &config, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
	if e != nil {
		t.Fatal(e)
	}
	if e = global.Enable(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = global.Close() })
	env, _ := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(h)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(config)}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: state, Environment: env, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		t.Fatal(e)
	}
	merged, e := startup.GetInitialSettings()
	if e != nil {
		t.Fatal(e)
	}
	if merged != state.GetSessionSettingsCache().Settings {
		t.Fatal("real cache identity lost")
	}
	owner := cooperative.New()
	t.Cleanup(owner.Close)
	gate, e := workflowcommands.NewGate(owner, startup, env)
	if e != nil {
		t.Fatal(e)
	}
	return owner, merged, gate
}
func originalInput() Input {
	return Input{Profile: NeutralBareProfile, Platform: "darwin", Model: "fixture-model", NonInteractive: true, BaseToolsCLI: []string{""}, Mode: "default"}
}
func TestNeutralRealPermissionInitialization(t *testing.T) {
	for _, content := range []string{"", `{"enableWorkflows":true}`, `{"disableWorkflows":true}`, `{"enableWorkflows":false}`, `{"disableWorkflows":false}`, `{"futureNull":null,"futureFalse":false,"futureArray":[null,false,3],"\ud800":"lone","\udfff":"low","10":"ten","2":"two","future":{"large":9007199254740993}}`} {
		t.Run(content, func(t *testing.T) {
			owner, merged, gate := setup(t, content)
			before, _ := merged.MarshalJSON()
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				state, e := Initialize(scope, owner, merged, gate, originalInput())
				if e != nil {
					return e
				}
				if !state.Owns(owner) || state.Owns(nil) {
					return fmt.Errorf("%s", fmt.Sprint("owner identity"))
				}
				got, permission, e := state.Read(scope)
				if e != nil {
					return e
				}
				if got != merged {
					return fmt.Errorf("%s", fmt.Sprint("settings reference copied"))
				}
				var rules struct {
					CLI []string `json:"cliArg"`
				}
				if e = json.Unmarshal(permission.Get("alwaysDenyRules"), &rules); e != nil {
					return e
				}
				has := false
				for _, name := range rules.CLI {
					if name == "Workflow" {
						has = true
					}
				}
				want := content != `{"disableWorkflows":true}` && content != `{"enableWorkflows":false}`
				if has != want {
					return fmt.Errorf("%s", fmt.Sprint("actual enabled registry deny projection", rules.CLI))
				}
				if permission.String("mode") != "default" || string(permission.Get("isAutoModeAvailable")) != "true" {
					return fmt.Errorf("%s", fmt.Sprint("real Source default permission state"))
				}
				permission.Set("mode", quote("plan"))
				_, again, e := state.Read(scope)
				if e != nil {
					return e
				}
				if again.String("mode") != "default" {
					return fmt.Errorf("%s", fmt.Sprint("permission snapshot mutates owner"))
				}
				merged.Set("futureLate", []byte(`false`))
				live, _, e := state.Read(scope)
				if e != nil {
					return e
				}
				if string(live.Get("futureLate")) != "false" {
					return fmt.Errorf("%s", fmt.Sprint("AppState live settings became clone"))
				}
				merged.Set("model", quote("other"))
				_, _, e = state.Read(scope)
				var cap *CapabilityError
				if !errors.As(e, &cap) {
					return fmt.Errorf("%s", fmt.Sprint("active setting mutation bypassed ASK consumer", e))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			merged.Delete("model")
			merged.Delete("futureLate")
			after, _ := merged.MarshalJSON()
			if string(before) != string(after) {
				t.Fatal("permission initializer altered unknown ordered settings")
			}
		})
	}
}
func TestNeutralKnownFieldsAndUTF16Classification(t *testing.T) {
	for _, raw := range []string{`{"model":null}`, `{"env":{}}`, `{"hooks":{}}`, `{"pluginConfigs":{}}`, `{"permissions":{}}`, `{"effortLevel":"low"}`, `{"cleanupPeriodDays":0}`, `{"disableWorkflows":null}`, `{"enableWorkflows":[]}`, `{"\u006dodel":false}`} {
		var value protocol.JSONObject
		if e := value.UnmarshalJSON([]byte(raw)); e != nil {
			t.Fatal(e)
		}
		before, _ := value.MarshalJSON()
		var cap *CapabilityError
		if !errors.As(ValidateNeutral(&value), &cap) {
			t.Fatal("active schema presence escaped capability", raw)
		}
		after, _ := value.MarshalJSON()
		if !reflect.DeepEqual(before, after) {
			t.Fatal("guard mutated raw values")
		}
	}
	for _, raw := range []string{`{"\ud800":null,"�":false,"\udfff":[]}`, `{"future":null}`} {
		var value protocol.JSONObject
		_ = value.UnmarshalJSON([]byte(raw))
		if e := ValidateNeutral(&value); e != nil {
			t.Fatal("genuine unknown misclassified", e)
		}
	}
	if e := ValidateNeutral(nil); e == nil {
		t.Fatal("nil object admitted")
	}
	var zero protocol.JSONObject
	if e := ValidateNeutral(&zero); e == nil {
		t.Fatal("non-object admitted")
	}
}
func TestNeutralProfileAndOwnerGuards(t *testing.T) {
	owner, merged, gate := setup(t, "")
	foreign := cooperative.New()
	defer foreign.Close()
	if _, e := Initialize(nil, owner, merged, gate, originalInput()); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := foreign.Run(context.Background(), func(s *cooperative.Scope) error {
		_, e := Initialize(s, owner, merged, gate, originalInput())
		return e
	}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		for _, change := range []func(*Input){func(i *Input) { i.Profile = "" }, func(i *Input) { i.Platform = "windows" }, func(i *Input) { i.Model = "other" }, func(i *Input) { i.NonInteractive = false }, func(i *Input) { i.Mode = "auto" }, func(i *Input) { i.AllowBypass = true }, func(i *Input) { i.Allowed = []string{"Read"} }, func(i *Input) { i.Disallowed = []string{"Write"} }, func(i *Input) { i.AdditionalDirectories = []string{"/"} }, func(i *Input) { i.BaseToolsCLI = nil }, func(i *Input) { i.BaseToolsCLI = []string{"default"} }} {
			i := originalInput()
			change(&i)
			var cap *CapabilityError
			_, e := Initialize(s, owner, merged, gate, i)
			if !errors.As(e, &cap) {
				return fmt.Errorf("%s", fmt.Sprint("unported init admitted", i, e))
			}
		}
		_, e := Initialize(s, owner, merged, nil, originalInput())
		var cap *CapabilityError
		if !errors.As(e, &cap) {
			return fmt.Errorf("%s", fmt.Sprint(e))
		}
		state, e := Initialize(s, owner, merged, gate, originalInput())
		if e != nil {
			return e
		}
		if _, _, e = state.Read(nil); !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("%s", fmt.Sprint(e))
		}
		var zero *State
		if _, _, e = zero.Read(s); !errors.Is(e, cooperative.ErrRestricted) {
			return fmt.Errorf("%s", fmt.Sprint(e))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
