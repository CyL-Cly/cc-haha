package clibootstrap

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	return Options{WorkingDir: home, TempDir: home}
}

func TestFirstEagerFlagValueBeforeCommander(t *testing.T) {
	for _, tc := range []struct {
		argv  []string
		value string
		found bool
	}{
		{nil, "", false}, {[]string{"--settings"}, "", false},
		{[]string{"--settings", "first", "--settings=second"}, "first", true},
		{[]string{"--settings=", "--settings", "second"}, "", true},
		{[]string{"--", "--settings", "after-separator"}, "after-separator", true},
		{[]string{"--settings", "--help"}, "--help", true},
		{[]string{"--settings-extra=x", "--settings=y"}, "y", true},
	} {
		value, found := EagerValue(tc.argv, "--settings")
		if value != tc.value || found != tc.found {
			t.Fatalf("%v => %q/%v", tc.argv, value, found)
		}
	}
}

func TestSettingsBytesHashStabilityAndFixedEffectsOrder(t *testing.T) {
	options := testOptions(t)
	args := []string{"--setting-sources=local,user,user", "--settings", " \ufeff{ \"unknown\":1,\"env\":{\"X\":\"untouched\"},\"emoji\":\"💡\" }\u00a0"}
	first := Prepare(args, options)
	second := Prepare(args, options)
	if first.ExitCode != 0 || first.State.FlagSettingsPath == nil || !reflect.DeepEqual(first, second) {
		t.Fatal(first, second)
	}
	data, err := os.ReadFile(*first.State.FlagSettingsPath)
	if err != nil || string(data) != `{ "unknown":1,"env":{"X":"untouched"},"emoji":"💡" }` {
		t.Fatal(string(data), err)
	}
	if !strings.HasPrefix(filepath.Base(*first.State.FlagSettingsPath), "claude-settings-") {
		t.Fatal(first.State)
	}
	if !reflect.DeepEqual(first.State.AllowedSettingSources, []string{"localSettings", "userSettings", "userSettings"}) || !reflect.DeepEqual(first.State.EnabledSettingSources, []string{"localSettings", "userSettings", "policySettings", "flagSettings"}) {
		t.Fatal(first.State)
	}
	want := []Event{{Operation: "set-flag-path", Value: *first.State.FlagSettingsPath}, {Operation: "reset-cache"}, {Operation: "set-sources", Value: []string{"localSettings", "userSettings", "userSettings"}}, {Operation: "reset-cache"}}
	if !reflect.DeepEqual(first.Events, want) {
		t.Fatal(first.Events)
	}
	failed := Prepare([]string{"--setting-sources=invalid", "--settings={}"}, options)
	if failed.ExitCode != 1 || failed.State.FlagSettingsPath == nil || len(failed.Events) != 2 {
		t.Fatal("earlier settings effects lost on later failure", failed)
	}
}

func TestSourcesEmptyInvalidAndInitialStateIsolation(t *testing.T) {
	options := testOptions(t)
	initialPath := "initial"
	initial := State{FlagSettingsPath: &initialPath, AllowedSettingSources: []string{"projectSettings"}}
	options.InitialState = &initial
	empty := Prepare([]string{"--settings=", "--setting-sources="}, options)
	if empty.ExitCode != 0 || len(empty.State.AllowedSettingSources) != 0 || !reflect.DeepEqual(empty.State.EnabledSettingSources, []string{"policySettings", "flagSettings"}) || *empty.State.FlagSettingsPath != initialPath {
		t.Fatal(empty)
	}
	if !reflect.DeepEqual(initial.AllowedSettingSources, []string{"projectSettings"}) {
		t.Fatal("caller initial state mutated")
	}
	for _, value := range []string{"USER", "user,", "\u0085user", " ", "--help"} {
		failed := Prepare([]string{"--setting-sources", value}, options)
		if failed.ExitCode != 1 || !strings.Contains(failed.Stderr, "Invalid setting source:") || len(failed.Events) != 0 || !reflect.DeepEqual(failed.State.AllowedSettingSources, initial.AllowedSettingSources) {
			t.Fatal(value, failed)
		}
	}
	valid := Prepare([]string{"--setting-sources", "\ufeffuser\u00a0,project,local"}, options)
	if valid.ExitCode != 0 || !reflect.DeepEqual(valid.State.AllowedSettingSources, []string{"userSettings", "projectSettings", "localSettings"}) {
		t.Fatal(valid)
	}
}

func TestExistingFilesAreReadBeforeSchemaValidation(t *testing.T) {
	options := testOptions(t)
	path := filepath.Join(options.WorkingDir, "invalid.json")
	if err := os.WriteFile(path, []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	result := Prepare([]string{"--settings", "invalid.json"}, options)
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || result.ExitCode != 0 || result.State.FlagSettingsPath == nil || *result.State.FlagSettingsPath != canonical {
		t.Fatal(result, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "not JSON" {
		t.Fatal("explicit settings file modified", err)
	}
	for _, tc := range []struct{ value, message string }{
		{"missing.json", "Error: Settings file not found: missing.json"},
		{"[]", "Error: Settings file not found: []"},
		{"{invalid}", "Error: Invalid JSON provided to --settings"},
		// Native errno differs across operating systems. The actual TS/Go
		// differential corpus checks full stdio only on its executing platform.
		{"invalid.json/child", "Error"},
		{options.WorkingDir, "EISDIR: illegal operation on a directory, read"},
	} {
		failed := Prepare([]string{"--settings", tc.value, "--setting-sources=local"}, options)
		if failed.ExitCode != 1 || !strings.Contains(failed.Stderr, tc.message) || len(failed.Events) != 0 {
			t.Fatal(tc.value, failed)
		}
	}
}

func TestInlineWriteFailureDoesNotSetStateOrResetCache(t *testing.T) {
	options := testOptions(t)
	options.TempDir = filepath.Join(options.TempDir, "missing")
	result := Prepare([]string{"--settings", "{}", "--setting-sources=local"}, options)
	if result.ExitCode != 1 || !strings.Contains(result.Stderr, "Error processing settings: ENOENT:") || result.State.FlagSettingsPath != nil || len(result.Events) != 0 {
		t.Fatal(result)
	}
}
