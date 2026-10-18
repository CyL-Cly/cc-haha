package tui

// The companion's rarity and pet-heart roles from the frozen six themes.
var terminalBuddyRoles = map[string]map[string]string{
	"dark":             {"inactive": "rgb(153,153,153)", "success": "rgb(78,186,101)", "autoAccept": "rgb(175,135,255)", "warning": "rgb(255,193,7)"},
	"light":            {"inactive": "rgb(102,102,102)", "success": "rgb(44,122,57)", "autoAccept": "rgb(135,0,255)", "warning": "rgb(150,108,30)"},
	"dark-daltonized":  {"inactive": "rgb(153,153,153)", "success": "rgb(51,153,255)", "autoAccept": "rgb(175,135,255)", "warning": "rgb(255,204,0)"},
	"light-daltonized": {"inactive": "rgb(102,102,102)", "success": "rgb(0,102,153)", "autoAccept": "rgb(135,0,255)", "warning": "rgb(255,153,0)"},
	"dark-ansi":        {"inactive": "ansi:white", "success": "ansi:greenBright", "autoAccept": "ansi:magentaBright", "warning": "ansi:yellowBright"},
	"light-ansi":       {"inactive": "ansi:blackBright", "success": "ansi:green", "autoAccept": "ansi:magenta", "warning": "ansi:yellow"},
}

func terminalBuddyRoleEscape(theme, role string) string {
	values, ok := terminalBuddyRoles[theme]
	if !ok {
		values = terminalBuddyRoles["dark"]
	}
	if value := values[role]; value != "" {
		return terminalThemeColorEscape(value, false)
	}
	if role == "" {
		return ""
	}
	return TerminalThemeRoleEscape(theme, role, false)
}
