package clibootstrap

import "github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"

func (m *MainPrint) nativeFilePermissionRoots() map[string]string {
	n := m.options.Native
	roots := map[string]string{"userSettings": n.Home, "projectSettings": n.CWD, "localSettings": n.CWD, "policySettings": n.ManagedRoot}
	if m.options.Platform != "windows" {
		return roots
	}
	// getSettingsRootPathForSource uses the relocated user config directory,
	// original cwd for policy/project/local and the explicit flag-file parent.
	original := n.CWD
	if d := m.options.DetachedAgent; d != nil && d.Bootstrap.OriginalCWD != "" {
		original = d.Bootstrap.OriginalCWD
	} else if m.state != nil && m.state.Snapshot().OriginalCWD != "" {
		original = m.state.Snapshot().OriginalCWD
	}
	config := n.ConfigHomeDirectory()
	if config == "" {
		config = nativepaths.JoinWindows(n.Home, ".claude")
	}
	roots["userSettings"] = nativepaths.ResolveWindows(config, n.CWD)
	for _, source := range []string{"projectSettings", "localSettings", "policySettings", "cliArg", "command", "session", "flagSettings"} {
		roots[source] = original
	}
	if flag := m.options.Arguments.Sources.FlagSettingsPath; flag != nil {
		roots["flagSettings"] = nativepaths.DirWindows(nativepaths.ResolveWindows(*flag, original))
	}
	return roots
}
