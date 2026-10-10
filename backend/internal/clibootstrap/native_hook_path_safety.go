package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"path/filepath"
)

// This is getSettingsPaths's actual source path producer, without reading
// settings contents or inventing a user/schema identity for safety checks.
func (m *MainPrint) nativeHookPathSafetyContext(scope *cooperative.Scope) (builtintools.HookPathSafetyContext, error) {
	if !scope.BelongsTo(m.Owner) {
		return builtintools.HookPathSafetyContext{}, cooperative.ErrRestricted
	}
	state := m.state.Snapshot()
	c := builtintools.HookPathSafetyContext{CWD: state.OriginalCWD, Platform: m.options.Platform, SettingsPaths: []string{}}
	if nativeEnvTruthy(m.options.Native.Environment["CLAUDE_CODE_USE_COWORK_PLUGINS"]) {
		return c, mainCap("actual cowork Hook settings path producer")
	}
	for _, source := range []string{"userSettings", "projectSettings", "localSettings"} {
		path, e := m.settings.Path(source, "")
		if e != nil {
			return c, e
		}
		c.SettingsPaths = append(c.SettingsPaths, path)
	}
	if path := m.options.Arguments.Sources.FlagSettingsPath; path != nil {
		c.SettingsPaths = append(c.SettingsPaths, *path)
	}
	c.SettingsPaths = append(c.SettingsPaths, filepath.Join(m.options.Native.ManagedRoot, "managed-settings.json"))
	return c, nil
}
func (m *MainPrint) nativeHookPermissionRoot(scope *cooperative.Scope, source string) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	state := m.state.Snapshot()
	switch source {
	case "userSettings":
		path, e := m.settings.Path(source, "")
		return filepath.Dir(path), e
	case "flagSettings":
		if path := m.options.Arguments.Sources.FlagSettingsPath; path != nil {
			return filepath.Dir(*path), nil
		}
		return state.OriginalCWD, nil
	case "projectSettings", "localSettings", "policySettings", "session", "command", "cliArg":
		return state.OriginalCWD, nil
	}
	return "", mainCap("Hook permission source " + source)
}
