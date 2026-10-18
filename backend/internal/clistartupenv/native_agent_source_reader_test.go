package clistartupenv

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func newActualAgentReaderTestService(t *testing.T, base *Service, flagPath *string, inline json.RawMessage) *Service {
	t.Helper()
	home, config := filepath.Dir(base.options.ConfigHome), base.options.ConfigHome
	project := filepath.Join(home, "project")
	environment, err := NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(config)}, protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}))
	if err != nil {
		t.Fatal(err)
	}
	state, err := clisettings.NewState(func() float64 { return 946684800000 })
	if err != nil {
		t.Fatal(err)
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: project, Profile: clisettings.NativeProcessProfile, Now: func() float64 { return 946684800000 }, PID: os.Getpid(), NativeUserSettingsPath: func() (string, error) {
		var current string
		if err := json.Unmarshal(environment.Get("CLAUDE_CONFIG_DIR"), &current); err != nil {
			return "", err
		}
		if !filepath.IsAbs(current) {
			current = filepath.Join(project, current)
		}
		return filepath.Join(current, "settings.json"), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	options := base.options
	options.Settings, options.SettingsState, options.Environment = settings, state, environment
	options.Host = Host{Profile: ResolvedPolicyReleaseProfile, PluginBaseAbsent: true, FlagPath: flagPath, FlagInline: inline, ResolvePolicy: func() (*protocol.JSONObject, error) {
		parsed, err := settings.ParseFile(filepath.Join(home, "managed", "managed-settings.json"))
		if err != nil {
			return nil, err
		}
		return parsed.Settings, nil
	}}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNativeAgentSourceReaderExactServicesAndOtherHostControls(t *testing.T) {
	base, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	if _, err := base.NativeAgentSettingsReader(base.options.Settings, base.options.SettingsState, base.options.Environment); err == nil {
		t.Fatal("finite source service acquired native Agent reader")
	}
	s := newActualAgentReaderTestService(t, base, nil, nil)
	read, err := s.NativeAgentSettingsReader(s.options.Settings, s.options.SettingsState, s.options.Environment)
	if err != nil {
		t.Fatal(err)
	}
	otherState, _ := clisettings.NewState(func() float64 { return 1 })
	otherEnvironment, _ := NewEnvironment(protocol.NewJSONObject())
	for _, args := range []struct {
		settings *clisettings.Service
		state    *clisettings.State
		env      *Environment
	}{{base.options.Settings, s.options.SettingsState, s.options.Environment}, {s.options.Settings, otherState, s.options.Environment}, {s.options.Settings, s.options.SettingsState, otherEnvironment}, {nil, s.options.SettingsState, s.options.Environment}} {
		if _, err := s.NativeAgentSettingsReader(args.settings, args.state, args.env); err == nil {
			t.Fatal("foreign or missing construction identity accepted")
		}
	}
	if _, err := read("unknown-source"); err == nil {
		t.Fatal("unknown source accepted")
	}
	for _, key := range []string{"HOME", "USER_TYPE", "CLAUDE_CODE_ENABLE_XAA", "CLAUDE_CODE_SIMPLE"} {
		original := s.options.Environment.Get(key)
		if err := s.options.Environment.SetProperty(quote(key), quote("changed")); err != nil {
			t.Fatal(err)
		}
		if _, err := read("userSettings"); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host "+key) {
			t.Errorf("native Agent reader admitted changed %s: %v", key, err)
		}
		if err := s.options.Environment.SetProperty(quote(key), original); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.options.Environment.SetProperty(quote("CLAUDE_CONFIG_DIR"), quote(filepath.Join(filepath.Dir(s.options.ConfigHome), "next"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NativeAgentSettingsReader(s.options.Settings, s.options.SettingsState, s.options.Environment); err == nil {
		t.Fatal("late reader binding bypassed initial full host admission")
	}
	if _, err := s.GetSettingsForSource("userSettings"); err == nil {
		t.Fatal("ordinary source getter lost its full host guard")
	}
	if _, err := s.GetInitialSettings(); err == nil {
		t.Fatal("ordinary initial settings getter lost its full host guard")
	}
	if err := s.assign(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote("changed-assignment")})); err == nil {
		t.Fatal("ordinary assignments lost frozen control admission")
	}
}

func TestNativeAgentSourceReaderColdFlagInlineMergeAgainstWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned whole first-cold Agent Source required")
	}
	base, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	home := filepath.Dir(base.options.ConfigHome)
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-agent-cold.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + base.options.ConfigHome, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CONFIG_DIR_AGENT_COLD_SOURCE=1", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_SIMPLE=0", "CLAUDE_CODE_USE_NATIVE_FILE_SEARCH=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(quoteFixture(map[string]string{"home": home, "profile": "inline"}))
	output, err := command.CombinedOutput()
	var source struct {
		Initial, Next, NextPhysical, NextSettingsPath string
		FlagPath                                      *string
		FlagInline, InitialSettings, NextSettings     json.RawMessage
		MutatedSettings                               json.RawMessage
		SourceSettings, ParsedNext                    protocol.JSONObject
		ColdAtHold                                    map[string]bool
		Caps                                          struct{ Blocked, SecureReads []string }
	}
	if err != nil || json.Unmarshal(output, &source) != nil || !source.ColdAtHold["flagSettings"] || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("whole cold Agent inline Source %v %.4000s", err, output)
	}
	for path, data := range map[string]json.RawMessage{filepath.Join(source.Initial, "settings.json"): source.InitialSettings, filepath.Join(source.NextPhysical, "settings.json"): source.NextSettings} {
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := newActualAgentReaderTestService(t, base, source.FlagPath, source.FlagInline)
	if _, err = s.GetInitialSettings(); err != nil {
		t.Fatal(err)
	}
	read, err := s.NativeAgentSettingsReader(s.options.Settings, s.options.SettingsState, s.options.Environment)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings"} {
		if _, present := s.options.SettingsState.GetCachedSettingsForSource(name); present {
			t.Fatalf("actual merged parser incorrectly warmed per-source %s", name)
		}
	}
	if err = s.options.Environment.SetProperty(quote("CLAUDE_CONFIG_DIR"), quote(source.Next)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"} {
		value, err := read(name)
		if err != nil {
			t.Fatal(name, err)
		}
		actual := json.RawMessage("null")
		if value != nil {
			actual, err = value.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
		}
		if string(protocol.StringifyValidatedJavaScriptJSON(actual)) != string(protocol.StringifyValidatedJavaScriptJSON(source.SourceSettings.Get(name))) {
			t.Errorf("%s actual native source differs Source Go%s Source%s", name, actual, source.SourceSettings.Get(name))
		}
		if name == "userSettings" {
			if err = os.WriteFile(filepath.Join(source.NextPhysical, "settings.json"), source.MutatedSettings, 0600); err != nil {
				t.Fatal(err)
			}
		}
		again, err := read(name)
		if err != nil || again != value {
			t.Errorf("%s source memo/identity changed %v", name, err)
		}
	}
}

func quoteFixture(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
