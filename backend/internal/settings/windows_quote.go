package settings

import "strings"

// Match the escaping contract used by the existing execa/cross-spawn boundary.
// cross-spawn's MIT notice is retained in CROSS_SPAWN_LICENSE.
func windowsBatchCommandLine(shell, executable string, args []string) string {
	command := escapeWindowsMeta(executable)
	normalized := strings.ReplaceAll(strings.ToLower(executable), `\`, "/")
	doubleEscape := strings.Contains(normalized, "/node_modules/.bin/") && strings.HasSuffix(normalized, ".cmd")
	for _, argument := range args {
		command += " " + windowsBatchArgument(argument, doubleEscape)
	}
	return `"` + shell + `" /d /s /c "` + command + `"`
}

func windowsBatchArgument(argument string, doubleEscape bool) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	backslashes := 0
	for _, character := range argument {
		if character == '\\' {
			backslashes++
			continue
		}
		if character == '"' {
			quoted.WriteString(strings.Repeat(`\`, backslashes*2+1))
		} else {
			quoted.WriteString(strings.Repeat(`\`, backslashes))
		}
		backslashes = 0
		quoted.WriteRune(character)
	}
	quoted.WriteString(strings.Repeat(`\`, backslashes*2))
	quoted.WriteByte('"')
	result := escapeWindowsMeta(quoted.String())
	if doubleEscape {
		result = escapeWindowsMeta(result)
	}
	return result
}

func escapeWindowsMeta(value string) string {
	var escaped strings.Builder
	for _, character := range value {
		if strings.ContainsRune("()[]%!^\"`<>&|;, *?", character) {
			escaped.WriteByte('^')
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}
