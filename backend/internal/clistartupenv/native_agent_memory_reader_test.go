package clistartupenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeAgentMemoryReaderPreservesCachesAndRejectsOtherHostChanges(t *testing.T) {
	base, _ := newTestService(t, `{"autoMemoryEnabled":true}`, protocol.NewJSONObject(), true)
	if _, _, err := base.NativeAgentMemorySettingsReader(base.options.Settings, base.options.SettingsState, base.options.Environment); err == nil {
		t.Fatal("finite settings acquired native memory reader")
	}
	s := newActualAgentReaderTestService(t, base, nil, nil)
	initial, err := s.GetInitialSettings()
	if err != nil {
		t.Fatal(err)
	}
	read, check, err := s.NativeAgentMemorySettingsReader(s.options.Settings, s.options.SettingsState, s.options.Environment)
	if err != nil {
		t.Fatal(err)
	}
	foreignState, _ := clisettings.NewState(func() float64 { return 1 })
	foreignEnvironment, _ := NewEnvironment(protocol.NewJSONObject())
	for _, foreign := range []struct {
		settings    *clisettings.Service
		state       *clisettings.State
		environment *Environment
	}{
		{base.options.Settings, s.options.SettingsState, s.options.Environment},
		{s.options.Settings, foreignState, s.options.Environment},
		{s.options.Settings, s.options.SettingsState, foreignEnvironment},
		{nil, s.options.SettingsState, s.options.Environment},
	} {
		if _, _, err := s.NativeAgentMemorySettingsReader(foreign.settings, foreign.state, foreign.environment); err == nil {
			t.Fatal("foreign source identity acquired memory reader")
		}
	}
	for _, key := range []string{"HOME", "USER_TYPE", "CLAUDE_CODE_ENABLE_XAA", "CLAUDE_CODE_SIMPLE"} {
		original := s.options.Environment.Get(key)
		if err := s.options.Environment.SetProperty(quote(key), quote("changed")); err != nil {
			t.Fatal(err)
		}
		if err := check(); err == nil || !strings.Contains(err.Error(), key) {
			t.Error("fast-path memory controls admitted unsupported host", key, err)
		}
		if _, err := read(); err == nil || !strings.Contains(err.Error(), key) {
			t.Error("merged memory reader admitted unsupported host", key, err)
		}
		if err := s.options.Environment.SetProperty(quote(key), original); err != nil {
			t.Fatal(err)
		}
	}
	next := filepath.Join(filepath.Dir(s.options.ConfigHome), "next-memory-settings")
	if err = os.MkdirAll(next, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(next, "settings.json"), []byte(`{"autoMemoryEnabled":false,"futureMemory":{"next":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.options.Environment.SetProperty(quote("CLAUDE_CONFIG_DIR"), quote(next)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.NativeAgentMemorySettingsReader(s.options.Settings, s.options.SettingsState, s.options.Environment); err == nil {
		t.Fatal("late binding skipped initial host admission")
	}
	warm, err := read()
	if err != nil || warm != initial {
		t.Fatal("directory mutation changed original warm merged identity", err)
	}
	s.options.SettingsState.ResetSettingsCache()
	fresh, err := read()
	if err != nil || fresh == warm || string(fresh.Get("autoMemoryEnabled")) != "false" || fresh.Get("futureMemory") == nil {
		t.Fatal("actual cache reset did not read current physical user file", fresh, err)
	}
	if _, err = s.GetInitialSettings(); err == nil {
		t.Fatal("ordinary merged host guard was relaxed")
	}
	if _, err = s.GetSettingsForSource("userSettings"); err == nil {
		t.Fatal("ordinary source host guard was relaxed")
	}
}
