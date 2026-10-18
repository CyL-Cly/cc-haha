package clisettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeAgentDirectoryBindingRejectsFiniteFixedAndForeignState(t *testing.T) {
	home := t.TempDir()
	state, err := NewState(func() float64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewState(func() float64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	options := Options{State: state, ConfigHome: home, OriginalCWD: home, Profile: ExternalReleaseProfile, Now: func() float64 { return 1 }, PID: os.Getpid()}
	finite, err := NewService(options)
	if err != nil {
		t.Fatal(err)
	}
	if finite.UsesNativeUserSettingsPath(state) {
		t.Fatal("finite service acquired native directory binding")
	}
	options.Profile = NativeProcessProfile
	fixed, err := NewService(options)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.UsesNativeUserSettingsPath(state) {
		t.Fatal("fixed native service acquired a current-directory producer")
	}
	options.NativeUserSettingsPath = func() (string, error) { return filepath.Join(home, "settings.json"), nil }
	native, err := NewService(options)
	if err != nil {
		t.Fatal(err)
	}
	if !native.UsesNativeUserSettingsPath(state) || native.UsesNativeUserSettingsPath(other) || native.UsesNativeUserSettingsPath(nil) || (*Service)(nil).UsesNativeUserSettingsPath(state) {
		t.Fatal("native exact-state construction identity failed")
	}
}
