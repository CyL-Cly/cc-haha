package windowshell

import "regexp"

var commandProcessor = regexp.MustCompile(`(?i)^(?:.*\\)?cmd(?:\.exe)?$`)

// ProjectWindowsShellInvocation is the actual shell:true Node child-process
// projection used by Source execa. A configured non-cmd processor uses -c;
// cmd.exe receives the unescaped command inside the original outer quotes.
func ProjectWindowsShellInvocation(command string, environment map[string]string) WindowsInvocation {
	shell := environmentValue(environment, "comspec")
	if shell == "" {
		shell = "cmd.exe"
	}
	if commandProcessor.MatchString(shell) {
		return WindowsInvocation{Command: shell, Arguments: []string{"/d", "/s", "/c", `"` + command + `"`}, Verbatim: true}
	}
	return WindowsInvocation{Command: shell, Arguments: []string{"-c", command}}
}
