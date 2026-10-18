package terminalcapabilities

import (
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func integerPrefix(value string) (int, bool) {
	value = protocol.TrimJavaScriptString(value)
	end := 0
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		end++
	}
	start := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == start {
		return 0, false
	}
	number, err := strconv.Atoi(value[:end])
	return number, err == nil
}

func hyperlinkVersion(value string) (int, int) {
	if len(value) == 3 || len(value) == 4 {
		digits := true
		for _, char := range value {
			digits = digits && char >= '0' && char <= '9'
		}
		if digits {
			minor, _ := strconv.Atoi(value[:len(value)-2])
			return 0, minor
		}
	}
	parts := strings.Split(value, ".")
	major, _ := integerPrefix(parts[0])
	minor := 0
	if len(parts) > 1 {
		minor, _ = integerPrefix(parts[1])
	}
	return major, minor
}

// LibraryHyperlinks preserves supports-hyperlinks' order. TTY and color are
// explicit OS observations; no terminal category grants an app permission.
func LibraryHyperlinks(env map[string]string, platform string, tty, color bool, args []string) bool {
	if force := env["FORCE_HYPERLINK"]; force != "" {
		value, number := integerPrefix(force)
		return !number || value != 0
	}
	flags := map[string]bool{}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		flags[arg] = true
	}
	if flags["--no-hyperlink"] || flags["--no-hyperlinks"] || flags["--hyperlink=false"] || flags["--hyperlink=never"] {
		return false
	}
	if flags["--hyperlink=true"] || flags["--hyperlink=always"] {
		return true
	}
	if env["NETLIFY"] != "" {
		return true
	}
	if !color || !tty {
		return false
	}
	if _, exists := env["WT_SESSION"]; exists {
		return true
	}
	if platform == "win32" || platform == "windows" || env["CI"] != "" || env["TEAMCITY_VERSION"] != "" {
		return false
	}
	major, minor := hyperlinkVersion(env["TERM_PROGRAM_VERSION"])
	switch env["TERM_PROGRAM"] {
	case "iTerm.app":
		return major == 3 && minor >= 1 || major > 3
	case "WezTerm":
		version := env["TERM_PROGRAM_VERSION"]
		if strings.HasPrefix(version, "0-unstable-") && validHyperlinkDate(strings.TrimPrefix(version, "0-unstable-")) {
			return strings.TrimPrefix(version, "0-unstable-") >= "2020-06-20"
		}
		return major >= 20200620
	case "vscode":
		return env["CURSOR_TRACE_ID"] != "" || major > 1 || major == 1 && minor >= 72
	case "ghostty", "zed":
		return true
	}
	if version := env["VTE_VERSION"]; version != "" {
		if version == "0.50.0" {
			return false
		}
		major, minor = hyperlinkVersion(version)
		return major > 0 || minor >= 50
	}
	return env["TERM"] == "alacritty" || env["TERM"] == "xterm-kitty"
}

func validHyperlinkDate(value string) bool {
	if len(value) != 10 {
		return false
	}
	for index, character := range value {
		if index == 4 || index == 7 {
			if character != '-' {
				return false
			}
		} else if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// SupportsHyperlinks is the original ink wrapper's additional detection. Its
// additions still apply when the base library returns false (including tmux).
func SupportsHyperlinks(env map[string]string, stdoutSupported bool) bool {
	if stdoutSupported {
		return true
	}
	additional := func(value string) bool {
		switch value {
		case "ghostty", "Hyper", "kitty", "alacritty", "iTerm.app", "iTerm2":
			return true
		}
		return false
	}
	return additional(env["TERM_PROGRAM"]) || additional(env["LC_TERMINAL"]) || strings.Contains(env["TERM"], "kitty")
}
