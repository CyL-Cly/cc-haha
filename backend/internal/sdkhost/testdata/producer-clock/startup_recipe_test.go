package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type originalConfigReadObserver struct {
	cliglobalconfig.OSFileSystem
	state         *clisettings.State
	reads         int
	settingsReady []bool
}

func (f *originalConfigReadObserver) ReadFile(path string) ([]byte, error) {
	f.reads++
	f.settingsReady = append(f.settingsReady, f.state.GetSessionSettingsCache() != nil)
	return f.OSFileSystem.ReadFile(path)
}

func originalRecipeServices(t *testing.T, globalRaw string) (*clistartupenv.Service, *cliglobalconfig.Service, *clisettings.State, *clistartupenv.Environment, *originalConfigReadObserver, string) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(globalRaw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"env":{"ORIGINAL_MARKER":"fresh"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	nowMillis := func() float64 { return 1700000000000 }
	now := func() time.Time { return time.UnixMilli(1700000000000).UTC() }
	logBase := filepath.Join(config, "diagnostics", "cli-events")
	process, err := diagnostics.NewProcess(diagnostics.Options{Environment: func(k string) string {
		if k == "CLAUDE_CODE_DIAGNOSTICS_FILE" {
			return logBase
		}
		return ""
	}, Now: now, NowMillis: func() int64 { return 1700000000000 }, PID: os.Getpid(), Platform: "darwin", Files: diagnostics.OSFileSystem{}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := clisettings.NewState(nowMillis)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: nowMillis, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	observed := &originalConfigReadObserver{state: state}
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: now, NowMillis: nowMillis, Diagnostics: process, FS: observed, Stderr: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := global.Close(); err != nil {
			t.Error(err)
		}
	})
	env, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(config)}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
	if err != nil {
		t.Fatal(err)
	}
	startup, err := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: state, Environment: env, ConfigHome: config, Diagnostics: process, NowMillis: nowMillis, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if err != nil {
		t.Fatal(err)
	}
	return startup, global, state, env, observed, logBase
}

func originalRecipeEvents(t *testing.T, logBase string) []string {
	t.Helper()
	paths, err := filepath.Glob(logBase + ".*.current.jsonl")
	if err != nil || len(paths) != 1 {
		t.Fatalf("native diagnostic files %v %v", paths, err)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for decoder.More() {
		var line struct {
			Event string `json:"event"`
		}
		if err := decoder.Decode(&line); err != nil {
			t.Fatal(err)
		}
		events = append(events, line.Event)
	}
	return events
}

func TestOriginalProgramSettingsBeforeConfigs(t *testing.T) {
	startup, global, state, env, observed, logBase := originalRecipeServices(t, `{}`)
	if err := initializeOriginalProgramSettings(startup, global, true); err != nil {
		t.Fatal(err)
	}
	want := []string{"settings_load_started", "settings_load_completed", "enable_configs_started", "enable_configs_completed"}
	if events := originalRecipeEvents(t, logBase); !reflect.DeepEqual(events, want) {
		t.Fatalf("actual original program order = %v; want %v", events, want)
	}
	if observed.reads == 0 {
		t.Fatal("global native read absent")
	}
	for _, ready := range observed.settingsReady {
		if !ready {
			t.Fatal("global read happened before original merged settings cache")
		}
	}
	cache := state.GetSessionSettingsCache()
	if cache == nil || cache.Settings == nil {
		t.Fatal("actual merged settings cache absent")
	}
	merged, err := startup.GetInitialSettings()
	if err != nil || merged != cache.Settings {
		t.Fatal("original cache identity", merged, err)
	}
	if env.Get("ORIGINAL_MARKER") != nil {
		t.Fatal("original getter prematurely applied settings environment")
	}
	if err := initializeOriginalProgramSettings(startup, global, true); err != nil {
		t.Fatal(err)
	}
	if events := originalRecipeEvents(t, logBase); !reflect.DeepEqual(events, want) {
		t.Fatal("idempotent recipe emitted extra diagnostic", events)
	}
}

func TestOriginalProgramConfigFailureAfterSettings(t *testing.T) {
	startup, global, state, _, _, logBase := originalRecipeServices(t, `{broken`)
	if err := initializeOriginalProgramSettings(startup, global, true); err == nil {
		t.Fatal("invalid global config admitted")
	}
	if state.GetSessionSettingsCache() == nil {
		t.Fatal("global config failure preceded original settings getter")
	}
	want := []string{"settings_load_started", "settings_load_completed", "enable_configs_started"}
	if events := originalRecipeEvents(t, logBase); !reflect.DeepEqual(events, want) {
		t.Fatal("failure emitted wrong actual diagnostic order", events)
	}
}

func TestOriginalProgramSettingsFailureStopsConfigs(t *testing.T) {
	startup, global, _, env, observed, _ := originalRecipeServices(t, `{}`)
	if err := env.SetProperty(quote("HOME"), quote("/changed-host")); err != nil {
		t.Fatal(err)
	}
	if err := initializeOriginalProgramSettings(startup, global, true); err == nil {
		t.Fatal("changed host admitted")
	}
	if observed.reads != 0 {
		t.Fatal("global config read despite original settings getter failure", observed.reads)
	}
}
