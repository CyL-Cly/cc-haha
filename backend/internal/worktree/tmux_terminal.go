package worktree

import "strings"

// The first two Source isInITerm2 probes override detectTerminal. Only the
// exact TERM fallback can additionally produce iTerm.app; every preceding
// environment detector below returns a different terminal name.
func IsTmuxITerm2(env map[string]string) bool {
	if env["TERM_PROGRAM"] == "iTerm.app" || env["ITERM_SESSION_ID"] != "" {
		return true
	}
	if env["TERM"] != "iTerm.app" || env["TERM_PROGRAM"] != "" {
		return false
	}
	if env["CURSOR_TRACE_ID"] != "" {
		return false
	}
	askpass := env["VSCODE_GIT_ASKPASS_MAIN"]
	for _, name := range []string{"cursor", "windsurf", "antigravity"} {
		if strings.Contains(askpass, name) {
			return false
		}
	}
	bundle := strings.ToLower(env["__CFBundleIdentifier"])
	for _, name := range []string{"vscodium", "windsurf", "com.google.android.studio", "pycharm", "intellij", "webstorm", "phpstorm", "rubymine", "clion", "goland", "rider", "datagrip", "appcode", "dataspell", "aqua", "gateway", "fleet", "jetbrains", "androidstudio"} {
		if strings.Contains(bundle, name) {
			return false
		}
	}
	if env["TERMINAL_EMULATOR"] == "JetBrains-JediTerm" {
		return false
	}
	for _, name := range []string{"VisualStudioVersion", "TMUX", "STY", "KONSOLE_VERSION", "GNOME_TERMINAL_SERVICE", "XTERM_VERSION", "VTE_VERSION", "TERMINATOR_UUID", "KITTY_WINDOW_ID", "ALACRITTY_LOG", "TILIX_ID", "WT_SESSION", "MSYSTEM", "ConEmuANSI", "ConEmuPID", "ConEmuTask", "WSL_DISTRO_NAME", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if env[name] != "" {
			return false
		}
	}
	return true
}
