package windowshell

import (
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
)

// WindowsInvocation is cross-spawn's non-shell command/argv projection after
// native PATH resolution. Cmd wrappers need verbatim cmd.exe arguments, while
// exe/com files keep their original argument vector.
type WindowsInvocation struct {
	Command   string
	Arguments []string
	Verbatim  bool
}

var cmdShim = regexp.MustCompile(`(?i)node_modules[\\/].bin[\\/][^\\/]+\.cmd$`)

func ProjectWindowsInvocation(command, commandFile string, arguments []string, environment map[string]string) WindowsInvocation {
	result := WindowsInvocation{Command: command, Arguments: append([]string(nil), arguments...)}
	if regexp.MustCompile(`(?i)\.(?:com|exe)$`).MatchString(commandFile) {
		return result
	}
	parts := []string{escapeWindowsCommand(nativepaths.CleanWindows(command))}
	for _, arg := range arguments {
		parts = append(parts, escapeWindowsArgument(arg, cmdShim.MatchString(commandFile)))
	}
	shell := environment["comspec"]
	if shell == "" {
		for key, value := range environment {
			if strings.EqualFold(key, "comspec") {
				shell = value
				break
			}
		}
	}
	if shell == "" {
		shell = "cmd.exe"
	}
	return WindowsInvocation{Command: shell, Arguments: []string{"/d", "/s", "/c", `"` + strings.Join(parts, " ") + `"`}, Verbatim: true}
}

func escapeWindowsCommand(value string) string {
	var out strings.Builder
	for _, r := range value {
		if strings.ContainsRune("()[]%!^\"`<>&|;, *?", r) {
			out.WriteByte('^')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func escapeWindowsArgument(value string, double bool) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	for i := 0; i < len(value); {
		if value[i] == '\\' {
			start := i
			for i < len(value) && value[i] == '\\' {
				i++
			}
			count := i - start
			if i == len(value) || value[i] == '"' {
				// The frozen cross-spawn regexp uses a lazy capture. Only the
				// final slash in a run is doubled; preserve its actual output.
				count++
			}
			quoted.WriteString(strings.Repeat(`\`, count))
			continue
		}
		if value[i] == '"' {
			quoted.WriteByte('\\')
		}
		quoted.WriteByte(value[i])
		i++
	}
	quoted.WriteByte('"')
	out := escapeWindowsCommand(quoted.String())
	if double {
		out = escapeWindowsCommand(out)
	}
	return out
}
