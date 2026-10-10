package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestConfiguredAgentLivePermissionOverlayMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	type row struct {
		Definition   map[string]any `json:"definition"`
		ParentEffort string         `json:"parentEffort"`
		Modes        []string       `json:"modes"`
	}
	cases := []row{}
	for _, source := range []string{"userSettings", "projectSettings", "localSettings", "built-in"} {
		for _, mode := range []string{"plan", "bypassPermissions"} {
			cases = append(cases, row{map[string]any{"source": source, "permissionMode": mode, "effort": "low"}, "high", []string{"default", "bypassPermissions", "acceptEdits", "plan", "default"}})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-runtime.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_AGENT_RUNTIME_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source live runtime %v %.2000s", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			States []struct {
				Mode, Effort, ParentMode string
				RulesPreserved           bool
			}
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source invalid/escaped %.2000s", out)
	}
	for i, example := range cases {
		c, _, owner := boundController(t)
		defer owner.Close()
		settingsState, _ := clisettings.NewState(func() float64 { return 0 })
		settings, err := clisettings.NewService(clisettings.Options{State: settingsState, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 0 }, PID: os.Getpid()})
		if err != nil {
			t.Fatal(err)
		}
		global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
		if err != nil {
			t.Fatal(err)
		}
		if err = global.Enable(); err != nil {
			t.Fatal(err)
		}
		defer global.Close()
		environment, _ := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(config)}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
		startup, err := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: settingsState, Environment: environment, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
		if err != nil {
			t.Fatal(err)
		}
		merged, err := startup.GetInitialSettings()
		if err != nil {
			t.Fatal(err)
		}
		gate, _ := workflowcommands.NewGate(owner, startup, environment)
		err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			permissions, err := sdkpermissions.Initialize(scope, owner, merged, gate, sdkpermissions.Input{Profile: sdkpermissions.PublicPrintProfile, NativeProcess: true, Platform: "darwin", Model: "fixture-model", BaseToolsCLI: []string{"Read"}, Mode: "default", AllowBypass: true})
			if err != nil {
				return err
			}
			if err = c.InitializeSDKSettings(scope, permissions); err != nil {
				return err
			}
			override := example.Definition["permissionMode"].(string)
			location := example.Definition["source"].(string)
			if (location == "projectSettings" || location == "localSettings") && override == "bypassPermissions" {
				override = ""
			}
			child := &Controller{options: Options{Bootstrap: c.options.Bootstrap, InheritedPermissions: c, AgentPermissionMode: override}}
			if err = child.InitializeSDKSettings(scope, permissions); err != nil {
				return err
			}
			if child.appState.permission != c.appState.permission {
				return fmt.Errorf("child detached parent permission identity")
			}
			for j, mode := range example.Modes {
				if err = permissions.SetMode(scope, mode); err != nil {
					return err
				}
				_, actual, err := child.SDKSettingsOnOwner(scope)
				if err != nil {
					return err
				}
				_, parent, err := c.SDKSettingsOnOwner(scope)
				if err != nil {
					return err
				}
				want := source.Rows[i].States[j]
				if actual.String("mode") != want.Mode || parent.String("mode") != want.ParentMode || !want.RulesPreserved {
					return fmt.Errorf("case%d transition%d actual%s parent%s Source%+v", i, j, actual.String("mode"), parent.String("mode"), want)
				}
				if !bytes.Equal(actual.Get("alwaysDenyRules"), parent.Get("alwaysDenyRules")) {
					return fmt.Errorf("child changed parent rules")
				}
				actual.Set("mode", quote("foreign"))
				_, again, _ := c.SDKSettingsOnOwner(scope)
				if again.String("mode") != mode {
					return fmt.Errorf("child returned mutable parent context")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
