package clistartupenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSDKSettingsReaderExactContextFreshControlAndOriginalCacheLifetimes(t *testing.T) {
	base, _ := newTestService(t, "{}", protocol.NewJSONObject(), true)
	if _, err := base.BindNativeSDKSettingsReader(base.options.Settings, base.options.SettingsState, base.options.Environment); err == nil {
		t.Fatal("finite service bound a native SDK reader")
	}
	if err := os.WriteFile(filepath.Join(base.options.ConfigHome, "settings.json"), []byte(`{"future":{"directory":"INITIAL"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := newActualAgentReaderTestService(t, base, nil, nil)
	reader, err := s.BindNativeSDKSettingsReader(s.options.Settings, s.options.SettingsState, s.options.Environment)
	if err != nil {
		t.Fatal(err)
	}
	if !reader.UsesSettingsContext(s, s.options.Settings, s.options.SettingsState, s.options.Environment) || reader.UsesSettingsContext(base, s.options.Settings, s.options.SettingsState, s.options.Environment) || new(NativeSDKSettingsReader).UsesSettingsContext(s, s.options.Settings, s.options.SettingsState, s.options.Environment) {
		t.Fatal("reader lost exact original context identity")
	}
	otherState, _ := clisettings.NewState(func() float64 { return 1 })
	otherEnvironment, _ := NewEnvironment(protocol.NewJSONObject())
	for _, args := range []struct {
		settings *clisettings.Service
		state    *clisettings.State
		env      *Environment
	}{{base.options.Settings, s.options.SettingsState, s.options.Environment}, {s.options.Settings, otherState, s.options.Environment}, {s.options.Settings, s.options.SettingsState, otherEnvironment}, {nil, s.options.SettingsState, s.options.Environment}} {
		if _, err := s.BindNativeSDKSettingsReader(args.settings, args.state, args.env); err == nil || reader.UsesSettingsContext(s, args.settings, args.state, args.env) {
			t.Fatal("foreign or missing settings graph accepted")
		}
	}
	before, err := reader.InitialSettings()
	if err != nil {
		t.Fatal(err)
	}
	oldSource, err := reader.SourceSettings("userSettings")
	if err != nil || before == nil || oldSource == nil {
		t.Fatal("original populated merged/source cache absent", before, oldSource, err)
	}
	next := filepath.Join(filepath.Dir(s.options.ConfigHome), "next")
	if err := os.MkdirAll(next, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "settings.json"), []byte(`{"future":{"directory":"NEXT"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.options.Environment.SetProperty(quote("CLAUDE_CONFIG_DIR"), quote(next)); err != nil {
		t.Fatal(err)
	}
	warm, err := reader.InitialSettings()
	warmSource, sourceErr := reader.SourceSettings("userSettings")
	if err != nil || sourceErr != nil || warm != before || warmSource != oldSource {
		t.Fatal("directory update reset the original merged/source caches", err, sourceErr)
	}
	fresh, err := reader.SettingsWithSources()
	effective, _ := fresh.Object("effective")
	future, _ := effective.Object("future")
	if err != nil || future.String("directory") != "NEXT" {
		t.Fatal("actual explicit settings control did not reload the current directory", fresh, err)
	}
	if _, err := s.GetInitialSettings(); err == nil {
		t.Fatal("ordinary startup getter lost its frozen directory guard")
	}
	if _, err := s.GetSettingsWithSources(); err == nil {
		t.Fatal("unbound control getter bypassed the frozen directory guard")
	}
	if _, err := s.BindNativeSDKSettingsReader(s.options.Settings, s.options.SettingsState, s.options.Environment); err == nil {
		t.Fatal("late binding skipped full initial admission")
	}
	if _, err := reader.SourceSettings("unknown"); err == nil {
		t.Fatal("unknown setting source accepted")
	}
	for _, key := range []string{"HOME", "USER_TYPE", "CLAUDE_CODE_ENABLE_XAA", "CLAUDE_CODE_SIMPLE"} {
		original := s.options.Environment.Get(key)
		if err := s.options.Environment.SetProperty(quote(key), quote("changed")); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.InitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host "+key) {
			t.Errorf("SDK reader admitted changed %s: %v", key, err)
		}
		if err := s.options.Environment.SetProperty(quote(key), original); err != nil {
			t.Fatal(err)
		}
	}
}
